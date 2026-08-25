package a

import (
	"fmt"
	"sync"

	"b"
)

// --- Receivers: reported ---

// Point is small and none of its pointer-receiver methods writes to it.
type Point struct { // want `methods of Point can use value receivers: Point is 16 bytes and none of its pointer-receiver methods writes to the receiver`
	X, Y int
}

func (p *Point) Len() int {
	return p.X*p.X + p.Y*p.Y
}

func (p *Point) String() string {
	return fmt.Sprintf("(%d, %d)", p.X, p.Y)
}

func (p *Point) Norm() int {
	return norm(p)
}

func norm(p *Point) int {
	return p.Len()
}

// Registry writes through a map, which a copy shares.
type Registry struct { // want `methods of Registry can use value receivers`
	items map[string]int
}

func (r *Registry) Add(key string) { // want Add:"recv=shared"
	r.items[key] = 1
}

// Tags1 writes through a slice, which a copy shares.
type Tags1 struct { // want `methods of Tags1 can use value receivers`
	tags []string
}

func (t *Tags1) Clear() { // want Clear:"recv=shared"
	t.tags[0] = ""
}

// Wrapper reads through an embedded pointer.
type Wrapper struct { // want `methods of Wrapper can use value receivers`
	*Inner
}

type Inner struct {
	name string
}

func (w *Wrapper) Name() string {
	return w.Inner.name
}

// --- Receivers: not reported ---

// Counter has a method that writes to the receiver.
type Counter struct {
	N int
}

func (c *Counter) Inc() { // want Inc:"recv=direct"
	c.N++
}

func (c *Counter) Get() int {
	return c.N
}

// Tags2 replaces the slice header, which is storage owned by the receiver.
type Tags2 struct {
	tags []string
}

func (t *Tags2) Add(s string) { // want Add:"recv=direct"
	t.tags = append(t.tags, s)
}

// Node returns its own pointer.
type Node struct {
	val int
}

func (n *Node) Self() *Node { // want Self:"recv=escape"
	return n
}

// Cfg writes through a helper.
type Cfg struct {
	x int
}

func (c *Cfg) Apply() { // want Apply:"recv=direct"
	mutate(c)
}

func mutate(c *Cfg) { // want mutate:"recv=none p0=direct"
	c.x = 1
}

// Printer passes its pointer to code we cannot see through.
type Printer struct {
	s string
}

func (p *Printer) Print() { // want Print:"recv=escape"
	fmt.Println(p)
}

// Locker must not be copied.
type Locker struct {
	mu sync.Mutex
	n  int
}

func (l *Locker) Get() int {
	return l.n
}

// Big is larger than the threshold.
type Big struct {
	data [40]string
}

func (b *Big) First() string {
	return b.data[0]
}

// Generic types are skipped.
type Generic[T any] struct {
	v T
}

func (g *Generic[T]) Get() T {
	return g.v
}

// NotFound is an error type.
type NotFound struct {
	Name string
}

func (e *NotFound) Error() string {
	return e.Name + " not found"
}

// Tree is a node of a linked structure; its pointer is its identity.
type Tree struct {
	Left, Right *Tree
	Val         int
}

func (t *Tree) Sum() int { // want Sum:"recv=shared\\+escape"
	if t == nil {
		return 0
	}

	return t.Val + t.Left.Sum() + t.Right.Sum()
}

// Cache is called on a nil receiver.
type Cache struct {
	n int
}

func (c *Cache) Get() int { // want Get:"recv=escape"
	if c == nil {
		return 0
	}

	return c.n
}

// Handle compares its pointer.
type Handle struct {
	id int
}

func (h *Handle) Same(o *Handle) bool { // want Same:"recv=escape p0=escape"
	return h == o
}

func (h *Handle) Key(m map[*Handle]int) int { // want Key:"recv=escape"
	return m[h]
}

// --- Returns: reported ---

// Config has no methods, so Config and *Config satisfy the same interfaces.
type Config struct {
	Host string
	Port int
}

func NewConfig() *Config { // want `NewConfig can return Config instead of \*Config: Config is 24 bytes and every return allocates a new value`
	return &Config{Host: "localhost"}
}

func newConfigWithNew() *Config { // want `newConfigWithNew can return Config instead of \*Config`
	return new(Config)
}

func LoadConfig(fail bool) (*Config, error) { // want `LoadConfig can return Config instead of \*Config`
	if fail {
		return &Config{}, fmt.Errorf("failed")
	}

	return &Config{Host: "localhost"}, nil
}

func NewPlain() *b.Plain { // want `NewPlain can return Plain instead of \*Plain`
	return &b.Plain{}
}

// --- Returns: not reported ---

var globalConfig = &Config{}

// FindConfig may return nil.
func FindConfig(ok bool) *Config {
	if !ok {
		return nil
	}

	return &Config{}
}

// LoadConfigOrNil returns nil on error.
func LoadConfigOrNil(fail bool) (*Config, error) {
	if fail {
		return nil, fmt.Errorf("failed")
	}

	return &Config{}, nil
}

// CachedConfig returns shared storage.
func CachedConfig() *Config {
	return globalConfig
}

type Server struct {
	cfg Config
}

func (s *Server) SetConfig(cfg Config) { // want SetConfig:"recv=direct"
	s.cfg = cfg
}

// ConfigOf returns a pointer into existing storage.
func ConfigOf(s *Server) *Config { // want ConfigOf:"recv=none p0=escape"
	return &s.cfg
}

// Config is a method; its signature may be dictated by an interface.
func (s *Server) Config() *Config {
	return &Config{}
}

// NewCounter returns a type with pointer-receiver methods.
func NewCounter() *Counter {
	return &Counter{}
}

// NewLocker returns a type that must not be copied.
func NewLocker() *Locker {
	return &Locker{}
}

// NewBig returns a type larger than the threshold.
func NewBig() *Big {
	return &Big{}
}

// NewTree returns a node of a linked structure.
func NewTree() *Tree {
	return &Tree{}
}

// NewNotFound returns an error type.
func NewNotFound() *NotFound {
	return &NotFound{}
}

// NewItem returns a type with pointer-receiver methods from another package.
func NewItem() *b.Item {
	return &b.Item{}
}

// viaCall does not allocate itself.
func viaCall() *Config {
	return NewConfig()
}

// named uses a bare return.
func named() (c *Config) {
	c = &Config{}

	return
}

// fromClosure returns whatever the closure returns.
func fromClosure() *Config {
	f := func() *Config { return &Config{} }

	return f()
}
