package threshold

type Small struct { // want `methods of Small can use value receivers: Small is 16 bytes`
	A, B int
}

func (s *Small) Sum() int {
	return s.A + s.B
}

type Medium struct {
	A, B, C int
}

func (m *Medium) Sum() int {
	return m.A + m.B + m.C
}

type Tiny struct {
	A int
}

func NewTiny() *Tiny { // want `NewTiny can return Tiny instead of \*Tiny: Tiny is 8 bytes`
	return &Tiny{}
}

type Wide struct {
	A, B, C int
}

func NewWide() *Wide {
	return &Wide{}
}
