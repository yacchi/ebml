module github.com/yacchi/ebml/impl/go/integrations/kvs

go 1.25

// A pseudo-version, not a replace: a replace in a DEPENDENCY's go.mod is
// ignored, so `v0.0.0` plus a replace made this module unresolvable for
// everyone outside the repository. The pin is a MINIMUM the way every Go
// requirement is, so it moves only when this module needs a newer core, and
// the root go.work is what makes local work see the tree instead of the proxy.
require github.com/yacchi/ebml/impl/go v0.0.0-20260726163710-6bae25947a98
