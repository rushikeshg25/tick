package tick

// Format is the shape every generator in this package shares: a named source
// of identifiers in canonical big-endian binary form.
//
// Big-endian matters. It is what makes bytewise comparison agree with the
// ordering of the fields the bytes encode, which is the whole reason a
// time-ordered ID helps a B-tree. The locality benchmark drives every format
// through this interface for exactly that reason.
type Format interface {
	// Name identifies the format in benchmark output.
	Name() string

	// NextBytes returns the next identifier as canonical big-endian bytes:
	// 8 for Snowflake-64, 16 for the UUID and ULID formats.
	NextBytes() ([]byte, error)
}

func (g *Generator) Name() string { return "snowflake64" }

func (g *Generator) NextBytes() ([]byte, error) {
	id, err := g.Next()
	if err != nil {
		return nil, err
	}
	v := uint64(id)
	return []byte{
		byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32),
		byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
	}, nil
}

func (g *UUIDv7Generator) Name() string { return "uuidv7" }

func (g *UUIDv7Generator) NextBytes() ([]byte, error) {
	u, err := g.Next()
	if err != nil {
		return nil, err
	}
	return u[:], nil
}

func (g *ULIDGenerator) Name() string { return "ulid" }

func (g *ULIDGenerator) NextBytes() ([]byte, error) {
	u, err := g.Next()
	if err != nil {
		return nil, err
	}
	return u[:], nil
}

// UUIDv4Source adapts the unordered version 4 UUID to Format so it can serve
// as the baseline in comparisons. It holds no state and needs no clock.
type UUIDv4Source struct{}

func (UUIDv4Source) Name() string { return "uuidv4" }

func (UUIDv4Source) NextBytes() ([]byte, error) {
	u := NewUUIDv4()
	return u[:], nil
}

var (
	_ Format = (*Generator)(nil)
	_ Format = (*UUIDv7Generator)(nil)
	_ Format = (*ULIDGenerator)(nil)
	_ Format = UUIDv4Source{}
)
