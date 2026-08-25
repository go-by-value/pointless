// Package b exercises facts imported across packages.
package b

type Item struct {
	N int
}

func (i *Item) Set(n int) { // want Set:"recv=direct"
	i.N = n
}

func (i *Item) Get() int {
	return i.N
}

// Plain has no methods, so Plain and *Plain satisfy the same interfaces.
type Plain struct {
	N int
}

var saved *Item

func Retain(i *Item) { // want Retain:"recv=none p0=escape"
	saved = i
}

func Read(i *Item) int {
	return i.N
}

func Write(i *Item) { // want Write:"recv=none p0=direct"
	i.N = 1
}
