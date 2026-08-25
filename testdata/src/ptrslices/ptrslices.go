package ptrslices

import "b"

type Item struct {
	N int
}

func (i Item) Value() int {
	return i.N
}

type Ptr struct {
	N int
}

func (p *Ptr) Inc() { // want Inc:"recv=direct"
	p.N++
}

func consume(items []*Item) {}

// --- Reported ---

func fresh() {
	items := make([]*Item, 0) // want `items can be \[\]Item instead of \[\]\*Item: Item is 8 bytes and the elements are never nil or used as pointers`
	items = append(items, &Item{N: 1}, new(Item))
	for i := range items {
		items[i].N++
	}
	_ = len(items)
}

func literal() {
	items := []*Item{{N: 1}, {N: 2}} // want `items can be \[\]Item instead of \[\]\*Item`
	_ = items[0].Value()
}

func readViaRange() {
	items := []*Item{{}} // want `items can be \[\]Item instead of \[\]\*Item`
	for _, it := range items {
		_ = it.N
	}
}

func declared() {
	var items []*Item // want `items can be \[\]Item instead of \[\]\*Item`
	items = append(items, &Item{})
	_ = items == nil
}

func Build() []*Item { // want `Build can return \[\]Item instead of \[\]\*Item`
	return []*Item{{N: 1}}
}

func BuildVar() []*Item { // want `BuildVar can return \[\]Item instead of \[\]\*Item`
	items := make([]*Item, 0) // want `items can be \[\]Item instead of \[\]\*Item`
	items = append(items, &Item{})

	return items
}

func Maybe(ok bool) []*Item { // want `Maybe can return \[\]Item instead of \[\]\*Item`
	if !ok {
		return nil
	}

	return []*Item{{}}
}

// --- Not reported ---

func withNilCheck() {
	items := make([]*Item, 3)
	if items[0] == nil {
		return
	}
}

func assignNil() {
	items := make([]*Item, 1)
	items[0] = nil
}

func mutateViaRange() {
	items := []*Item{{}}
	for _, it := range items {
		it.N = 1
	}
}

func escapeElement() {
	items := []*Item{{}}
	p := items[0]
	_ = p
}

func passed() {
	items := []*Item{{}}
	consume(items)
}

func appendExisting(p *Item) { // want appendExisting:"recv=none p0=escape"
	items := []*Item{}
	items = append(items, p)
	_ = items
}

func fromCall() {
	items := Build()
	_ = items
}

func pointerMethods() {
	xs := []*Ptr{{}}
	_ = xs
}

func FromOtherPackage() []*b.Plain {
	return []*b.Plain{{}}
}

var global []*Item
