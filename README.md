# pointless

[![CI](https://github.com/go-by-value/pointless/actions/workflows/ci.yaml/badge.svg)](https://github.com/go-by-value/pointless/actions/workflows/ci.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-by-value/pointless.svg)](https://pkg.go.dev/github.com/go-by-value/pointless)
[![Release](https://img.shields.io/github/v/release/go-by-value/pointless)](https://github.com/go-by-value/pointless/releases/latest)

pointless is a Go analyzer that suggests values instead of pointers for small structs.

```go
type Point struct{ X, Y int } // pointless: methods of Point can use value receivers: Point is 16 bytes and none of its pointer-receiver methods writes to the receiver

func (p *Point) Len() int { return p.X*p.X + p.Y*p.Y }
func (p *Point) String() string { return fmt.Sprintf("(%d, %d)", p.X, p.Y) }
```

Pointers are the default in a lot of Go code, but a pointer buys something only when the code relies on pointer semantics: mutation visible to the caller, identity, nil, or avoiding a large copy. When none of those apply, a value is simpler to reason about, cannot be nil, and avoids a heap allocation. pointless finds the places where the pointer is not doing anything. The name is the pun you expect.

## What it reports

**Pointer receivers that never need the pointer.** By default, a type is reported once when none of its pointer-receiver methods writes to the receiver, retains it, or compares it:

```go
type Registry struct{ items map[string]int }

func (r *Registry) Add(key string) { r.items[key] = 1 } // writes through the map, which a copy shares
func (r *Registry) Has(key string) bool { _, ok := r.items[key]; return ok }
```

Set `receivers: method` to report each method instead, if your codebase is fine with mixing receiver kinds within a type.

**Functions that return `*T` where every return allocates a fresh `T`:**

```go
func NewConfig() *Config { // pointless: NewConfig can return Config instead of *Config
    return &Config{Host: "localhost"}
}
```

Only reported when `T` has no pointer-receiver methods, so that `T` and `*T` satisfy the same interfaces and the change cannot break an implementation.

**Slices of pointers whose elements are never used as pointers** (`slices: true`, off by default):

```go
items := make([]*Item, 0) // pointless: items can be []Item instead of []*Item
items = append(items, &Item{N: 1})
```

## What it does not report

Whether a method writes to its receiver, and whether a function writes through or retains a pointer parameter, is analyzed across packages and shared as facts, so calling `bytes.Buffer.Len` on a field is fine while `bytes.Buffer.WriteString` is a write. A pointer is considered necessary when the code:

- Writes to the pointee itself: fields, nested structs, array elements, or a slice header (`u.Tags = append(u.Tags, x)`). Writes through pointer, slice, or map fields are shared with a copy and do not count.
- Stores, returns, or converts the pointer, passes it to a function value, an interface method, or an interface-typed parameter, or compares it (`p == q`, `p == nil`, use as a map key).
- Calls a method or a function whose behavior is unknown: no source, assembly, `unsafe`.

Types are skipped entirely when they are:

- Larger than the threshold (256 bytes by default; see below).
- Generic.
- Not safe to copy: containing `sync.Mutex`, `sync.WaitGroup`, `atomic.*`, or any type with a pointer-receiver `Lock` method, the same rule `go vet`'s `copylocks` uses.
- Error types (`*T` implements `error`). Go convention keeps one receiver kind for errors, and `errors.As` depends on which form the error was created in, so pointless leaves them alone.
- Nodes of a linked structure: a struct with a field referring to `*T`, such as `Left, Right *Tree`. Their pointers are identities.
- Declared in generated files (`// Code generated ... DO NOT EDIT.`).

For return types and slices, pointless additionally requires every return or element to be a fresh `&T{...}` or `new(T)`, never `nil`, never a pointer into existing storage, and the slice checks only local variables whose elements are never taken out as pointers.

## A suggestion, not a proof

pointless sees the declaration and the package it lives in, not the callers in other packages. A report means the pointer is not needed *inside* the declaration. Callers may still depend on it:

```go
cfg := NewConfig()
server.cfg = cfg
reloader.cfg = cfg // both expect to see reloader's later writes to cfg
```

Treat a report as "check whether anything relies on this being a pointer; if not, the value is simpler." This is why pointless is a style linter rather than a bug finder, and why `slices` is off by default.

## Threshold

The default threshold is 256 bytes. Below that, copying a struct costs about as much as one non-inlined function call, and returning a fresh struct by value is cheaper than allocating it at every size measured:

| Struct size | Value receiver call | Pointer receiver call | Return by value | Return by heap pointer |
|---|---|---|---|---|
| 64 B | 1.2 ns | 0.5 ns | 3.8 ns | 9.8 ns |
| 256 B | 3.5 ns | 0.5 ns | 11.7 ns | 75 ns |
| 1 KB | 11.8 ns | 0.5 ns | 40 ns | 138 ns |

(Apple M5, Go 1.27; the allocation column understates the cost because GC work grows with the number of live objects.) 256 bytes is 32 words, about ten to sixteen fields of strings and slices, which covers most domain structs. If you also run gocritic's `hugeParam` (80 bytes by default), align the two thresholds to avoid contradictory advice.

## Installation

```sh
go install github.com/go-by-value/pointless/cmd/pointless@latest
```

Prebuilt binaries are available on the [releases page](https://github.com/go-by-value/pointless/releases).

## Usage

```sh
pointless ./...
pointless -threshold 128 -receivers method -slices ./...
```

| Flag | Default | Meaning |
|---|---|---|
| `-threshold` | `256` | Largest struct size in bytes suggested as a value |
| `-receivers` | `type` | `type` reports a type once when all of its pointer-receiver methods can be values; `method` reports each method |
| `-returns` | `true` | Check pointer return types |
| `-slices` | `false` | Check slices of pointers |

pointless is a standard `go/analysis` analyzer, so it also works as a `go vet` tool:

```sh
go vet -vettool=$(which pointless) ./...
```

### golangci-lint

pointless ships a [module plugin](https://golangci-lint.run/docs/plugins/module-plugins/). Add it to `.custom-gcl.yml`:

```yaml
version: v2.13.0
plugins:
  - module: github.com/go-by-value/pointless
    import: github.com/go-by-value/pointless/plugin
    version: v0.1.0
```

Build the custom binary with `golangci-lint custom` and enable the linter in `.golangci.yaml`. Settings are optional; omitted keys keep their defaults:

```yaml
version: "2"
linters:
  enable:
    - pointless
  settings:
    custom:
      pointless:
        type: module
        description: Suggests values instead of pointers for small structs.
        settings:
          threshold: 256
          receivers: type
          returns: true
          slices: false
```

## Development

```sh
make test  # go test -race ./...
make lint  # golangci-lint run
make vet   # run pointless on its own source
```

## License

[MIT](./LICENSE)
