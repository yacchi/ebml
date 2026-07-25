package main

import (
	"fmt"
	"io"

	"github.com/yacchi/ebml/matroska"
	"github.com/yacchi/ebml/parser"
)

func scan(r io.Reader, h parser.Handler) error {
	s := parser.NewScanner(h, matroska.KindForElementID)
	buf := make([]byte, 64*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if feedErr := s.Feed(buf[:n]); feedErr != nil {
				return fmt.Errorf("at offset %d: %w", s.Offset(), feedErr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
	}
	if err := s.Finalize(); err != nil {
		return fmt.Errorf("at offset %d: %w", s.Offset(), err)
	}
	return nil
}
