package core

// Hash3 is the three-layer anchor hash (spec §1 "낡음 판정"): signature,
// body, file. Values are hex digests produced by the anchor resolver.
type Hash3 struct {
	Signature string `json:"signature"`
	Body      string `json:"body"`
	File      string `json:"file"`
}

// HashChange is the outcome of comparing two Hash3 values, from none to the
// most significant layer that differs.
type HashChange int

const (
	HashNone      HashChange = iota // identical
	HashFile                        // only the file changed (elsewhere in the file)
	HashBody                        // the symbol body changed, signature intact
	HashSignature                   // the signature changed: always stale
)

func (c HashChange) String() string {
	switch c {
	case HashNone:
		return "none"
	case HashFile:
		return "file"
	case HashBody:
		return "body"
	case HashSignature:
		return "signature"
	}
	return "unknown"
}

// Cmp returns the most significant layer in which b differs from a.
func (a Hash3) Cmp(b Hash3) HashChange {
	switch {
	case a.Signature != b.Signature:
		return HashSignature
	case a.Body != b.Body:
		return HashBody
	case a.File != b.File:
		return HashFile
	}
	return HashNone
}
