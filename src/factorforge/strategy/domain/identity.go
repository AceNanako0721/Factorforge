package domain

import "encoding/json"

// ApiScope and WorkloadCapability deliberately have different named types and
// validators. A public principal cannot be decoded as a signal authority.
type ApiScope string

const (
	Query       ApiScope = "query"
	Research    ApiScope = "score:research"
	ObjectWrite ApiScope = "object:write"
)

func (s *ApiScope) UnmarshalJSON(data []byte) error {
	var value string
	if json.Unmarshal(data, &value) != nil {
		return &Error{"API_SCOPE_INVALID", 422}
	}
	switch ApiScope(value) {
	case Query, Research, ObjectWrite:
		*s = ApiScope(value)
		return nil
	}
	return &Error{"API_SCOPE_INVALID", 422}
}

type WorkloadCapability string

const (
	SignalSIM  WorkloadCapability = "signal:sim"
	SignalLIVE WorkloadCapability = "signal:live"
)

func (s *WorkloadCapability) UnmarshalJSON(data []byte) error {
	var value string
	if json.Unmarshal(data, &value) != nil {
		return &Error{"WORKLOAD_CAPABILITY_INVALID", 422}
	}
	switch WorkloadCapability(value) {
	case SignalSIM, SignalLIVE:
		*s = WorkloadCapability(value)
		return nil
	}
	return &Error{"WORKLOAD_CAPABILITY_INVALID", 422}
}

type PublicPrincipal struct {
	PrincipalID string     `json:"principal_id"`
	InstanceID  string     `json:"instance_id"`
	Environment string     `json:"environment"`
	Scopes      []ApiScope `json:"scopes"`
}
type WorkloadIdentity struct {
	WorkloadID   string               `json:"workload_id"`
	InstanceID   string               `json:"instance_id"`
	Environment  string               `json:"environment"`
	ObjectIDs    []string             `json:"object_ids"`
	Capabilities []WorkloadCapability `json:"capabilities"`
}
