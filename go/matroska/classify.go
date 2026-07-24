package matroska

import "github.com/yacchi/ebml-reader/parser"

// KindForElementID classifies registered elements for parser.WithKindClassifier.
// The parser has no distinct kinds for non-binary scalar values, so every
// registered non-master type other than TypeUint is represented as a binary leaf.
// Unknown IDs use the parser's default binary leaf kind.
func KindForElementID(id parser.ElementID) parser.Kind {
	info, ok := Lookup(id)
	if !ok {
		return parser.KindBinary
	}
	if info.Type == TypeMaster {
		return parser.KindMaster
	}
	if info.Type == TypeUint {
		return parser.KindUint
	}
	return parser.KindBinary
}
