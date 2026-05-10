package v1alpha1

import "github.com/awslabs/operatorpkg/status"

type LXDNodeClassStatus struct {
	Conditions []status.Condition `json:"conditions,omitempty"`
}

func (in *LXDNodeClass) StatusConditions() status.ConditionSet {
	return status.NewReadyConditions().For(in)
}

func (in *LXDNodeClass) GetConditions() []status.Condition {
	return in.Status.Conditions
}

func (in *LXDNodeClass) SetConditions(conditions []status.Condition) {
	in.Status.Conditions = conditions
}
