package errors

// Domain is a type definition for specifying the error domain which is required
// in error details.
type Domain string

const (
	// DomainDataplane defines the string for the proto error domain "dataplane".
	DomainDataplane Domain = "redpanda.com/dataplane"
	// DomainControlplane defines the string for the proto error domain "controlplane".
	DomainControlplane Domain = "redpanda.com/controlplane"
)
