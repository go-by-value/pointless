// Command pointless suggests values instead of pointers for small structs.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/go-by-value/pointless"
)

func main() {
	singlechecker.Main(pointless.NewAnalyzer(pointless.DefaultSettings()))
}
