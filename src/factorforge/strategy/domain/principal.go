package domain

type Identity interface {
	Instance() string
	Env() string
	Payload() map[string]any
	Worker() *WorkloadIdentity
}

func (p PublicPrincipal) Instance() string           { return p.InstanceID }
func (p PublicPrincipal) Env() string                { return p.Environment }
func (p PublicPrincipal) Payload() map[string]any    { return Map(p) }
func (p PublicPrincipal) Worker() *WorkloadIdentity  { return nil }
func (p WorkloadIdentity) Instance() string          { return p.InstanceID }
func (p WorkloadIdentity) Env() string               { return p.Environment }
func (p WorkloadIdentity) Payload() map[string]any   { return Map(p) }
func (p WorkloadIdentity) Worker() *WorkloadIdentity { return &p }
func ScopeName(scope ApiScope) string {
	switch scope {
	case Query:
		return "ApiScope.QUERY"
	case Research:
		return "ApiScope.RESEARCH"
	case ObjectWrite:
		return "ApiScope.OBJECT"
	}
	return string(scope)
}
