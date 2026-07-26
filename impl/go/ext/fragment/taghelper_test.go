package fragment_test

import (
	"github.com/yacchi/ebml/impl/go/ext/fragment"
	"github.com/yacchi/ebml/impl/go/ext/tags"
)

// fragTag and fragTags are what Fragment.Tag and Fragment.Tags used to be, kept
// here as test helpers rather than as methods. ext/fragment offers no tag
// accessor of its own: a fragment's Segment is an ordinary retained element and
// ext/tags reads one, which is the arrangement that leaves ext/fragment
// importing nothing under ext/. Only this file imports ext/tags, so a test
// elsewhere in the package may still call a local variable "tags".
func fragTag(f *fragment.Fragment, name string) (string, bool) {
	if f == nil {
		return "", false
	}
	return tags.Read(f.Segment).Get(tags.Target{}, name)
}

func fragTags(f *fragment.Fragment) map[string]string {
	if f == nil {
		return make(map[string]string)
	}
	return tags.Read(f.Segment).All(tags.Target{})
}
