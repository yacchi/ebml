package writer_test

import (
	"bytes"
	"fmt"

	"github.com/yacchi/ebml/impl/go/writer"
)

// Example writes a small document to an in-memory sink. The element IDs are
// arbitrary well-formed EBML IDs; real code passes the matroska.ID* constants,
// since the writer itself knows no element.
func Example() {
	var buf bytes.Buffer
	w := writer.New(&buf)

	var errs []error
	record := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	record(w.StartMaster(0x4001, writer.Buffered())) // size emitted on EndMaster
	record(w.Uint(0x80, 1))
	record(w.String(0x83, "hi"))
	record(w.EndMaster())
	record(w.Close())

	if len(errs) > 0 {
		fmt.Println("errors:", errs)
		return
	}
	fmt.Printf("% X\n", buf.Bytes())
	// Output:
	// 40 01 87 80 81 01 83 82 68 69
}
