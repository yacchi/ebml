---
name: conformance-check
description: Check the hand-written go/matroska registry against the official IETF CELLAR EBML Schema documents. Fetches the schemas on demand (they are never vendored), runs internal/specconform, and turns the result into defects to fix and coverage to add. Use when broadening Matroska element coverage, after editing go/matroska/elements.go or the containment lists, or when asked for a spec conformance / 適合性チェック.
---

# Matroska/EBML conformance check

The registry in `go/matroska` is hand-written on purpose. This skill is what
keeps it honest: it compares the registry against the normative machine-readable
schemas and separates two very different results.

* **MISMATCH** — the registry states something the schema contradicts. A defect.
  Fix it before doing anything else.
* **GAP** — the registry is silent where the schema speaks. Never wrong (an
  unregistered element still reads as a binary leaf), but it is the worklist for
  broadening coverage.

## Standing rules

* The schema documents are **CC-BY-4.0 works of the IETF CELLAR working group
  and are never committed to this repository.** Fetch them into the gitignored
  cache, use them, leave them there.
* Never copy schema prose. Element `<documentation>` text is the part CC-BY
  actually covers; IDs, names, types and paths are interoperability facts.
  Doc comments in `go/matroska` are written from scratch, in this repository's
  own voice.
* Do not generate `elements.go`. The omissions are deliberate (see the
  containment invariant below), and generation would erase the intent.

## 1. Fetch the schemas

```bash
mkdir -p .spec-cache
curl -sSL -o .spec-cache/ebml.xml \
  https://raw.githubusercontent.com/ietf-wg-cellar/ebml-specification/master/ebml.xml
curl -sSL -o .spec-cache/ebml_matroska.xml \
  https://raw.githubusercontent.com/ietf-wg-cellar/matroska-specification/master/ebml_matroska.xml
```

`.spec-cache/` is gitignored. `master` is used deliberately — the point is to
notice upstream movement — but record what was checked, because these URLs are
the one link-rot risk this workflow carries:

```bash
gh api repos/ietf-wg-cellar/matroska-specification/commits/master --jq .sha
gh api repos/ietf-wg-cellar/ebml-specification/commits/master --jq .sha
```

Last verified: `matroska-specification` at `f93ab02`, `ebml-specification` at
`a4b3c4a` (schema `docType="matroska" version="4"`, 273 distinct elements;
registry at 270 of them, 0 mismatches).
If a fetch fails, the same files are reachable from
`https://github.com/ietf-wg-cellar/{ebml,matroska}-specification` and the
underlying specifications are RFC 8794 (EBML) and RFC 9559 (Matroska).

## 2. Run the check

```bash
go -C go run ./internal/specconform/checkschema \
  -schema ../.spec-cache/ebml.xml \
  -schema ../.spec-cache/ebml_matroska.xml
```

Both schemas are needed: `ebml.xml` declares the EBML header elements and
`ebml_matroska.xml` the body. The command exits non-zero on any MISMATCH.

* `-v` also lists the divergences this repository declares on purpose
  (`TypeBlock` for SimpleBlock/Block, the deprecated Cluster children).
* `-missing` prints the coverage worklist, grouped by parent master.

## 3. Act on the result

**Mismatches first.** Each one is a real defect. The checks are:

| check | invariant |
|---|---|
| `identity` | every registered ID has the schema's name, and every name resolves to the schema's ID |
| `value-type` | the registered `ValueType` is the schema's type, modulo declared refinements |
| `containment` | a `LegalChildren` list documented as COMPLETE matches the schema's direct children |
| `unknown-size` | every master the schema allows an unknown size for has a complete child list |
| `global` | the elements the registry never lets end a master are exactly the schema's global elements |
| `header-limits` | `parser.MaxElementIDLength` / `MaxElementSizeLength` match the schema's range for `EBMLMaxIDLength` / `EBMLMaxSizeLength` |
| `unassigned` | the IDs `internal/ebmltest` reserves as "no registry knows this" are absent from the schema |
| `path-consistency` | this checker's own path parsing agrees with the schema's `recursive` attribute |

The sharpest one is `containment`, and it has two halves.

A complete child list may omit a schema child **only while that element is also
unregistered** — `EndsUnknownSizeMaster` refuses to end a master on an
unregistered ID, so an unregistered omission can never cause a premature
boundary. This is why registering a new element is not a local change:
**registering a Cluster or Segment child without adding it to `completeChildren`
breaks the unknown-size boundary rule.** The checker reports that as a MISMATCH.

Unregistered is not on its own a good enough reason, either. A child the schema
still declares CURRENT is one a conforming writer emits, so leaving it out of
both the list and the registry means this library reads a legal file as a
nameless leaf inside a master it cannot reason about — reported as a GAP. Only a
child the schema marks REMOVED (`maxver` below the schema's own version) is a
deliberate omission, and that is read from `maxver`, never from a comment
claiming the element is deprecated.

**Then coverage.** Extend the registry one master at a time, in the grouping
`-missing` prints:

1. Add the `ID…` constant in `go/matroska/elements.go`, ordered as the
   surrounding block is.
2. Add the `elements` entry with the ValueType the schema's type maps to
   (`uinteger`→`TypeUint`, `utf-8`→`TypeUTF8`, `string`→`TypeString`,
   `integer`→`TypeInt`, `date`→`TypeDate`, `float`→`TypeFloat`,
   `master`→`TypeMaster`, `binary`→`TypeBinary`).
3. If it is a child of `Segment` or `Cluster`, add it to `completeChildren` in
   the same change. The checker will fail if you don't.
4. Registering a master means the cursor now DESCENDS into it. Check whether any
   fixture or golden trace covers it; regenerate goldens if the event stream
   changes.
5. Write the doc comment yourself. Do not paste the schema's documentation.

Re-run the check, then `go -C go test ./...` and `go -C go vet ./...`.

## 4. Report

State the schema commit checked, the mismatch count, and coverage as
`registered / declared`. When gaps were closed, say which masters' children were
added and what is still missing.

## What this does NOT check

This is an ELEMENT-level check. It verifies identity, type, containment and the
two header limits — and the report ends with the inventory of what it never
looked at, so a clean run is not mistaken for full spec conformance:

| schema declares | elements | what the library would need |
|---|---|---|
| `maxOccurs` / `minOccurs` | 241 | cardinality validation |
| `minver` | 113 | DocTypeVersion gating |
| `default` | 77 | resolving an absent element to its default |
| `range` | 71 | value-range validation |
| `restriction`/`enum` | 29 | enumerated value names |
| `length` | 12 | fixed payload-length validation |
| `recurring` | 3 | the once-per-Segment rule |
| `webmproject.org` extension | 133 | the WebM profile subset |

Each row is a CAPABILITY the library does not have, not a hole in the checker:
the checker can only compare a schema statement against a statement this library
makes, and for these it makes none. Adding one means designing the library
feature first; the check then follows for free.

## Not covered by this skill

* The Matroska test files (`ietf-wg-cellar/matroska-test-files`, ~185 MB, no
  license statement in the repository) are not used here. Real-file validation
  is a separate, opt-in exercise.
* `matroska_tags.xml` (the official tag definitions) is not checked; `go/integrations/kvs`
  carries vendor tag knowledge that has no schema counterpart.
