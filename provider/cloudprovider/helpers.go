package k3slxd

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/scheduling"

	"github.com/sliman/k3s-lxd-provider/apis/v1alpha1"
)

var (
	awsRegexp  = regexp.MustCompile(`^\w+\.(nano|micro|small|medium|large|\d*xlarge|metal)$`)
	familyDelim = regexp.MustCompile(`[.-]`)
)

type Offering struct {
	cloudprovider.Offering
	Requirements []corev1.NodeSelectorRequirement `json:"requirements"`
}

type InstanceTypeOptions struct {
	Name             string              `json:"name"`
	Offerings        []Offering          `json:"offerings"`
	Architecture     string              `json:"architecture"`
	OperatingSystems []corev1.OSName     `json:"operatingSystems"`
	Resources        corev1.ResourceList `json:"resources"`
}

//go:embed instance_types.json
var defaultRawInstanceTypes []byte

func ConstructInstanceTypes(ctx context.Context) ([]*cloudprovider.InstanceType, error) {
	var instanceTypes []*cloudprovider.InstanceType
	var opts []InstanceTypeOptions

	raw := defaultRawInstanceTypes
	if customPath := os.Getenv("INSTANCE_TYPES_FILE_PATH"); customPath != "" {
		data, err := os.ReadFile(customPath)
		if err != nil {
			return nil, fmt.Errorf("reading custom instance types: %w", err)
		}
		raw = data
	}

	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil, fmt.Errorf("parsing instance types JSON: %w", err)
	}

	for _, o := range opts {
		instanceTypes = append(instanceTypes, newInstanceType(o))
	}
	return instanceTypes, nil
}

func toLXDMemory(s string) string {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return s
	}
	bytes := q.Value()
	miB := bytes / (1024 * 1024)
	if miB < 1 {
		return "1MiB"
	}
	return fmt.Sprintf("%dMiB", miB)
}

func parseSizeFromType(ty, cpu string) string {
	if matches := awsRegexp.FindStringSubmatch(ty); matches != nil {
		return matches[1]
	}
	return cpu
}

func parseFamilyFromType(instanceType string) string {
	if instanceType == "" {
		return ""
	}
	familySplit := familyDelim.Split(instanceType, 2)
	if len(familySplit) < 2 {
		return instanceType[0:1]
	}
	return familySplit[0]
}

func newInstanceType(opts InstanceTypeOptions) *cloudprovider.InstanceType {
	var cpu, memory string
	for res, q := range opts.Resources {
		switch res {
		case corev1.ResourceCPU:
			cpu = q.String()
		case corev1.ResourceMemory:
			memory = q.String()
		}
	}

	instanceTypeLabels := map[string]string{
		v1alpha1.InstanceSizeLabelKey:   parseSizeFromType(opts.Name, cpu),
		v1alpha1.InstanceFamilyLabelKey: parseFamilyFromType(opts.Name),
		v1alpha1.InstanceCPULabelKey:    cpu,
		v1alpha1.InstanceMemoryLabelKey: memory,
	}

	opts.Resources = lo.Assign(corev1.ResourceList{
		corev1.ResourcePods: resource.MustParse("110"),
	}, opts.Resources)

	osNames := lo.Map(opts.OperatingSystems, func(os corev1.OSName, _ int) string { return string(os) })

	zones := lo.Uniq(lo.Flatten(lo.Map(opts.Offerings, func(o Offering, _ int) []string {
		req, _ := lo.Find(o.Requirements, func(req corev1.NodeSelectorRequirement) bool {
			return req.Key == corev1.LabelTopologyZone
		})
		return req.Values
	})))
	capacityTypes := lo.Uniq(lo.Flatten(lo.Map(opts.Offerings, func(o Offering, _ int) []string {
		req, _ := lo.Find(o.Requirements, func(req corev1.NodeSelectorRequirement) bool {
			return req.Key == v1.CapacityTypeLabelKey
		})
		return req.Values
	})))

	requirements := scheduling.NewRequirements(
		scheduling.NewRequirement(corev1.LabelInstanceTypeStable, corev1.NodeSelectorOpIn, opts.Name),
		scheduling.NewRequirement(corev1.LabelArchStable, corev1.NodeSelectorOpIn, opts.Architecture),
		scheduling.NewRequirement(corev1.LabelOSStable, corev1.NodeSelectorOpIn, osNames...),
		scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zones...),
		scheduling.NewRequirement(v1.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, capacityTypes...),
		scheduling.NewRequirement(v1alpha1.InstanceSizeLabelKey, corev1.NodeSelectorOpIn, instanceTypeLabels[v1alpha1.InstanceSizeLabelKey]),
		scheduling.NewRequirement(v1alpha1.InstanceFamilyLabelKey, corev1.NodeSelectorOpIn, instanceTypeLabels[v1alpha1.InstanceFamilyLabelKey]),
		scheduling.NewRequirement(v1alpha1.InstanceCPULabelKey, corev1.NodeSelectorOpIn, instanceTypeLabels[v1alpha1.InstanceCPULabelKey]),
		scheduling.NewRequirement(v1alpha1.InstanceMemoryLabelKey, corev1.NodeSelectorOpIn, instanceTypeLabels[v1alpha1.InstanceMemoryLabelKey]),
	)

	return &cloudprovider.InstanceType{
		Name:         opts.Name,
		Requirements: requirements,
		Offerings: lo.Map(opts.Offerings, func(off Offering, _ int) *cloudprovider.Offering {
			return &cloudprovider.Offering{
				Requirements: scheduling.NewRequirements(lo.Map(off.Requirements, func(req corev1.NodeSelectorRequirement, _ int) *scheduling.Requirement {
					return scheduling.NewRequirement(req.Key, req.Operator, req.Values...)
				})...),
				Price:     off.Price,
				Available: off.Available,
			}
		}),
		Capacity: opts.Resources,
		Overhead: &cloudprovider.InstanceTypeOverhead{
			KubeReserved: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
	}
}
