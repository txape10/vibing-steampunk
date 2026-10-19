package adt

// Unsearched is one thing a read could not look at, and why. Reason is the
// failure as it came back, not a category: the reader needs to know whether
// it was an authorisation, a timeout or a missing object. It means "could
// not look", never "looked, and here is a caveat".
type Unsearched struct {
	Object string `json:"object"`
	Reason string `json:"reason"`
}
