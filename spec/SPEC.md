# Streaming EBML Cursor Specification

This document specifies the portable core of `ebml`: a behavioral contract for
incremental EBML readers. The READING SURFACE is an explicitly pull-driven
cursor with distinguishable events and flow control, never a document-model
interface. Around it the core also specifies the element registry and
classifier, the retained element model, and the byte-supply contract a
source-owning driver honours.

A package belongs to that core when a port must agree on it to interoperate.
Ways of USING the core -- the packages under `impl/go/ext/` -- are outside this
contract, and another language may shape them differently or omit them.

## Relation to existing models

The reading core is an explicitly **pull-driven cursor**, closer to the cursor
flavour of StAX than to a callback reader. Input is pushed as chunks, and one
event is acquired at a time; the cursor reports need-more-data instead of
blocking on a stream. The former SAX-shaped callback layer is gone. On each
header, the consumer decides whether to descend and whether a leaf payload is
materialised, while keeping its state in local variables. The retained model
plays the DOM role and is specified in its own section below, while the reading
surface remains cursor-shaped. A port may implement the cursor without offering
retention; the conformance rules state which requirements then apply.

The analogy is inexact in several EBML-specific ways. Explicit element sizes
make extents first-class, so skipping a subtree is arithmetic rather than a
scan for a matching end tag. The end of a known-size master is therefore
**computed**, not discovered, and is observable before any enclosing
unknown-size master closes. Unknown-size masters have no XML analogue and need
the structural-termination rule. Element naming and value typing live in a
separate registry rather than in the parser. Finally, split invariance is
normative here because an implementation does not own the read loop.

## Portability of API shape

This document specifies an OBSERVABLE CONTRACT. How a port reproduces it is the
port's own business, and no mechanism used by the reference implementation is
itself a requirement. A port is expected to spell the contract the way its own
standard library would: nothing outside that standard library, no configuration
object, no hook registry, no query language, and errors, naming and lifetimes
named the way the host language names them. Go stamps a generation into every
node and panics on a stale one because Go has no borrow checker; a port whose
language expresses the same lifetime in its type system reproduces section 3's
guarantee with no stamp and no panic, and that is full conformance, not a
divergence. `io.Reader` is likewise Go's spelling of a byte source, not the
contract.

One shape is NOT free, because it is the contract rather than its spelling: the
ARITY OF A PULL. Acquiring an event has three outcomes -- an event,
need-more-data, and end of input -- and a two-outcome iterator protocol
(value/done) can carry them only by collapsing one of them. The test for a port
is therefore not "does it avoid iterators" but "can this protocol state all three
outcomes without lying", and the answer follows from WHO OWNS THE BYTE SUPPLY:

* The caller supplies the bytes (section 2's cursor). Need-more-data has nowhere
  to go, so acquiring an event stays an explicit operation. An iterator-family
  protocol that does carry a third, not-yet state -- Rust's `Stream::poll_next`,
  where `Poll::Pending` IS need-more-data -- passes the test and is a correct
  spelling; a plain two-outcome iterator is not.
* The driver owns the source (section 5). Blocking or awaiting absorbs
  need-more-data before the consumer can observe it, only two outcomes remain,
  and an iterator, generator or async iterator is then the correct spelling.

A port that offers only an iterator over a caller-supplied cursor has dropped
this specification's central distinction, whatever else it gets right.
`docs/pull-shape-across-languages.md` works the two cases through several
languages.

## Writing

A conforming writer emits an element ID VINT, a size VINT, and the payload in
that order. An element ID is encoded in its complete 1-to-4-byte VINT form.
The size VINT uses 1-to-8 bytes; its value bits encode the payload length, and a
size may use any width from its minimal form up to the maximum width when the
value fits. A non-minimal width is legal, allowing a size to be reserved and
patched later without shifting the payload.

The all-ones size VINT is the unknown-size marker. It is valid for masters only.
The resulting master has no declared end: it terminates structurally when a
consumer-supplied boundary rule identifies an element that cannot be its child,
or at end of input during finalization.

Value payload encodings are the exact inverses of the reading side: unsigned
integers use big-endian binary representations, and signed integers use
big-endian two's-complement representations; floats use
IEEE 754 binary32 or binary64; ASCII strings and UTF-8 strings contain their
encoded characters without a terminator; dates are signed nanoseconds relative
to `2001-01-01T00:00:00 UTC`; and binary values are opaque bytes. The writer
does not infer any of these meanings from an element ID: the caller supplies
the value type.

Because the inverse is exact, a value a reader could not return unchanged is
refused rather than encoded. A string value must therefore not contain a NUL
byte: a reader stops at the first NUL, since EBML permits a string payload to be
zero-padded, so a writer rejects a string carrying one. Trailing zero padding is
a property of a payload, not of a value, and arbitrary bytes are a binary value.
A date is refused for the same reason once it leaves the range a signed 64-bit
nanosecond count reaches — roughly ±292 years around the epoch. Clamping such a
date to the nearest representable instant would read back as a different date,
so a writer reports it instead. An implementation must compute the offset in a
way that detects the overflow: a duration type that saturates at its own bounds
silently produces the wrong instant.

A conforming writer should offer three size strategies:

* **Buffer then size:** retain a master's payload, then emit its exact size.
  This works with any sink but uses memory proportional to the subtree.
* **Reserve and patch:** emit a fixed-width size placeholder, stream the
  payload, then patch the size. The sink must support positional patching
  (unless an enclosing buffer supplies it), and the final size must fit the
  reserved width.
* **Unknown size:** emit the unknown-size marker and stream the payload without
  patching. This requires a master and a consumer that implements the
  structural-termination rule.

These strategies are alternatives for masters; a complete leaf payload may use
the minimal size or an explicitly selected legal width. A reading-only
implementation need not provide writing.

> **Go mapping (non-normative):** package `writer` provides `Writer`,
> `StartMaster`, `EndMaster`, `Buffered`, `Reserved`, `UnknownSize`, and the
> value methods `Uint`, `Int`, `Float`, `String`, `UTF8`, `Date`, `Binary`, and
> `Leaf`. Package `tree` provides `Marshal` and `MarshalBytes`.

### SimpleBlock payload codec

A Matroska SimpleBlock's payload is itself a normative payload codec nested
inside one binary leaf value, distinct from the surrounding EBML structure: a
track-number VINT, a signed 16-bit relative timecode, one flags byte, and one or
more frames. Unlaced, the payload after the flags byte is the single frame in
full. Laced, a frame-count byte follows the flags byte (the encoded count is one
less than the actual count), then a size table in the form the declared lacing
prescribes, then the concatenated frame bytes, with the last frame's size implied
by what remains of the payload. Four lacing forms exist: no lacing, one frame;
Xiph lacing, each non-last frame's size as a run of value-255-terminated bytes;
fixed lacing, no size table at all because every frame but the last shares one
size; and EBML lacing, the first frame's size as a VINT and each following size
as a VINT-encoded signed delta from the previous frame's size.

An implementation that offers writing should provide the inverse encoder
alongside its SimpleBlock decoder. Because a decoder may accept liberalizations a
byte-exact re-encoding cannot reproduce — a non-minimal size VINT, or a reserved
flag bit — re-encoding a decoded value is a CANONICAL encoding rather than
necessarily byte-identical to arbitrary input: the canonical encoding of a laced
block parses back to a decoded value equal to the original, and is byte-identical
to the original payload whenever that payload was already canonical. The
conformance corpus's SimpleBlock payloads are all canonical, so for every one of
them decode-then-encode round-trips byte for byte.

> **Go mapping (non-normative):** `parser.ParseSimpleBlock` decodes a SimpleBlock
> payload into `*parser.SimpleBlock`; `(*parser.SimpleBlock).Append` is its exact
> inverse. Every SimpleBlock payload in the fixture corpus round-trips byte for
> byte through the pair; a laced block re-encodes to the canonical form of an
> equal decoded value.

## 1. Data model

An element is an element ID VINT, a size VINT, and a payload. A master payload is
a sequence of child elements. A leaf payload is opaque bytes to the cursor.
Offsets are absolute byte offsets from the beginning of the logical stream,
zero-based.

### VINT encoding

A VINT begins with a length marker: the first `1` bit in the first byte, counting
from the most significant bit, determines the total VINT length. A one-byte VINT
starts with `1`, a two-byte VINT with `01`, and so on. A first byte of `00`
would require nine bytes.

Element IDs use 1 through 4 bytes. The ID value is the complete encoded byte
sequence, including its length-marker bits. An ID VINT longer than 4 bytes is
rejected as an over-long element-ID VINT.

Element sizes use 1 through 8 bytes. The length marker is removed from the size
value; the remaining `7 * length` bits are the size value. An encoded size whose
value bits are all `1` means unknown size. A size VINT longer than 8 bytes is
rejected as an over-long element-size VINT.

The cursor must not interpret incomplete VINTs as elements. It reports that more
input is needed until the complete header is available.

### Unknown size

Unknown size is valid for a master. It has no declared end. It is invalid for a
leaf because the cursor cannot locate the next element after an unbounded leaf;
the cursor reports an unknown-size-leaf error while examining that header.

An unknown-size master closes in one of two ways:

* A consumer-supplied boundary rule may close it when the header of an element
  that cannot be its child is available. The close is structural and occurs
  before that element is processed at the enclosing depth.
* End of input closes all remaining unknown-size masters during finalization.

The boundary rule is evaluated against only the innermost open unknown-size
master. Each pull closes exactly one level. The triggering element's header is
not consumed until every level that must close has closed, so that element is
reported once, at the depth that remains.

Boundary detection must never scan payload bytes for the EBML header magic.
Payload data may contain that byte sequence.

Containment-based boundary rules must be deny-only over fully enumerated child
lists: an element may end an unknown-size master only when the registry knows it
is registered and cannot be a direct child (global elements do not end it).
Unknown or unlisted elements must not end the master. A false boundary corrupts
the parse, while a missed boundary only delays closure.

## 2. Cursor contract

The cursor accepts append-only chunks. A chunk may end anywhere, including in an
element ID, size, or payload. Results must not depend on chunk boundaries.

The conceptual operations are:

* **Feed** appends bytes without changing the logical element position.
* **Peek** reports the next element header or the end of a known-size master
  without consuming bytes. It reports a need-more-data condition when the next decision
  cannot yet be made.
* **Consume header** consumes the most recently peeked element header and makes
  its payload current.
* **Enter master** begins reading the current master and pushes it onto the
  enclosing-master stack.
* **Leave master** closes the current master at its declared end.
* **Close master** explicitly closes the innermost open master at the current
  offset, without waiting for a declared end. Its precondition: the innermost
  master either declares an unknown size, or is a known-size master whose
  declared end offset has already been reached. Closing a known-size master
  whose declared end has not been reached must be rejected as a distinct error
  and must leave the cursor unchanged; honouring it would discard a boundary the
  stream declares and read the master's remaining payload as elements of the
  enclosing master, corrupting every subsequent depth and offset.
* **Skip payload** advances over the current leaf payload without delivering
  bytes. A current master may instead be skipped as a complete subtree when its
  size is known.
* **Read payload** delivers the complete current leaf payload. Delivery may wait
  across multiple input chunks.
* **Finalize EOF** declares that no more bytes will arrive, flushes complete
  input, reports truncation for an incomplete header or declared payload, and
  closes remaining unknown-size masters.
* **Unconsumed** returns a copy of fed but unconsumed bytes for optional recovery
  after a terminal structural error. A resumed cursor may use a supplied start
  offset to preserve absolute offsets.

Operations that do not fit the current cursor state are invalid. A cursor that
reports a terminal structural error remains failed. A consumer's own verdict about
an element's content is a distinct class a consumer must be able to separate from
it; see "Error classification" in section 8.

## 3. Event model

Event acquisition is a **pull operation**: each operation reports one next event,
or one of the non-event outcomes of section 8: need-more-data, end of input, or a
structural failure. There are exactly three distinguishable event kinds:

* a **master start** (its header), on which the consumer decides descend or skip
  subtree;
* a **leaf header**, on which it decides materialise payload or skip payload;
* a **master end**, for every master that was entered.

A skipped master has no descendant events and no end event. Every event carries:

| Field | Meaning |
| --- | --- |
| element ID | Complete encoded ID VINT, including marker bits |
| kind | The classifier's verdict for this element (`master`, `uint`, `binary`, or `unknown` when it does not recognize the ID), which is what decides whether a header is a master or a leaf event; `end_master` on a master end. It must be reported, not merely used: an implementation that told the consumer only which of the three events this is would hide every distinction the classifier drew among leaves |
| depth | Number of enclosing entered masters; top-level is zero |
| header start offset | Absolute offset of the first header byte |
| header length | Encoded ID length plus encoded size length |
| declared size | Payload length, or unknown size |
| end offset | One past the declared element extent when known; unknown until a master closes when size is unknown |

The kind on an event is the **structural** verdict, and that division of labour is
deliberate. It is the classifier's answer that the reader itself acted on — master
versus leaf, plus the end-of-master marker — and it is reported so a consumer can
see what the element was read by. It is **not** the value type to interpret a
payload as: a classifier only has to separate masters from leaves for a stream to be
read correctly, so its leaf kinds are whatever it chose to distinguish and no more.
A consumer that wants to **decode** a payload asks the registry of section 7 for the
element's value type, never the event's kind, since an unlisted or deliberately
opaque element classifies as a binary or unknown leaf and that says nothing about
the bytes.

A master-end event carries the closed master's identity, depth, header location,
declared size, and the concrete offset at which it closed. For a known-size master
this is observable immediately when its declared payload bytes have been consumed,
regardless of any enclosing unknown-size master.

The enclosing-master chain is deliberately **not** part of an event: a pull
consumer that needs ancestry maintains it in its own loop, pushing on a descended
master and popping on its end, which the depth on every event makes verifiable.
An implementation must not require a per-event allocation to report ancestry.

An event **is** invalidated by the following pull, so the values above must be read
(or copied) before then. An implementation must state that rule and **must detect**
every use of an invalidated event, without exception: every operation an event offers
— the field accessors above exactly as much as the flow-control decisions of section 6
— is required to reject a use that happens after the following event has been
acquired. The requirement does not depend on how the consumer held the event, nor on
what the current event is: a handle the implementation delivered, an independent copy
of it, and a handle held while the current event is of the **same kind** as the one it
names are all invalidated alike. Silently answering such a use, so that a consumer
observes the values of a later event through a handle it took earlier, is prohibited.
Rejection reports a consumer defect and is therefore not one of the outcomes of
section 8: no further input can repair it, and the position of the next event is
unaffected.

Total detection constrains delivery. An event must be delivered either as an
independent value or as a handle onto storage the implementation does not reuse for a
later event, because a handle onto **reused** storage *is* that storage: once it has
been refilled for a later event of the same kind, the handle denotes the live event
and nothing distinguishes it from a handle taken for that event, so the detection
required above becomes impossible. Reuse therefore may not be used to avoid a
per-event allocation here. That allocation is bounded — one event's worth of identity
and extent, no payload and no ancestry — and buying detection with it is the trade
this section mandates: an undetectable stale event lets a consumer read one element's
values believing them to be another's, which no error path can later reveal. An
implementation that also offers a lower-level surface reporting each header as a
plain value, which hands out no event object at all, must say so, since that is where
a consumer unwilling to pay goes.

Delivered payload bytes (section 6) carry the same lifetime. They may alias the
implementation's own buffer, so they are valid only until the following event is
acquired and must not be modified; a consumer that needs them beyond that, or needs
to change them, copies them first. An implementation must not expose its own record
of a delivered payload: repeating the request within the event's lifetime must
reproduce the delivered bytes, and a consumer that modifies them must not be able to
alter the reader's state.

> **Go mapping (non-normative):** `parser.Cursor` with `Next`, `Feed`, `Finalize`,
> `Offset`, `Depth`, `Unconsumed` and `Err`, and no iterator: a range loop cannot
> distinguish need-more-data from end of input, and that distinction is normative
> here, so `Next` keeps it explicit. `Err` reports the last such outcome without
> advancing. The event is a
> closed `parser.Node` interface over `*MasterNode` (`Descend`, `Skip`), `*LeafNode`
> (`Payload`, `Skip`) and `*EndNode` (`Start`), so an operation invalid for an event
> cannot be written: a leaf payload cannot be requested from a master. The
> classifier's verdict is `Node.Kind`, a `parser.Kind` on all three variants
> (`KindEndMaster` on an `*EndNode`); a consumer decoding a payload takes its value
> type from `matroska.TypeFor` instead. Invalidation is
> a generation counter the cursor bumps on `Next` and `Finalize` — never on `Feed`,
> since a node has to survive the chunk that completes its payload — and stamps into
> the node it hands out; every exported node method, the extent accessors included,
> compares the stamp and panics on a mismatch. `Next` allocates a new node per event,
> so a node the cursor has moved past keeps its own stamp and every retention of it is
> caught: the `*MasterNode`/`*LeafNode`/`*EndNode` pointer the cursor returned, a node
> value the consumer copied (`v := *node`), and either of those while the current event
> is of the same variant. The measured price is one allocation per event and nothing
> else — reading every accessor adds none, and `Payload` adds none — asserted in
> `parser/node_validity_test.go` and priced against the low-level `parser.Parser`, which
> reports each header as an `ElementHeader` value, by `BenchmarkCursorScan` and
> `BenchmarkParserScan`. Read what you need, or copy the node, before pulling again.
> `LeafNode.Payload` returns a view of the cursor's buffer and keeps only that view's
> extent, never the slice it handed out.

## 4. Retained element model

A retained element preserves its element ID, the absolute offset of its header,
the header length (the encoded length of the ID VINT plus the size VINT), its
declared size, and either an owned payload for a leaf or ordered child elements
for a master. Children are in stream order.

The header length is retained as observed and is never recomputed. This
preserves parse-then-marshal byte identity: a legal but non-minimal size-VINT
width is retained rather than normalised away.

A retained leaf payload is owned by the element. It is a copy, not a view of the
reader's buffer; unlike a delivered reader-buffer view, it is not limited to
the next pull. Retention is never gated on a registry lookup: an element unknown
to the registry is retained like any other, and a master-shaped payload retained
as opaque bytes can be re-parsed later.

When a retention cap elides an element's payload, the element is marked
truncated. Its extent remains readable even though its bytes are not.

Retained access has two orthogonal modes. **Loose** extraction returns every
matching element regardless of containment. **Strict** access uses exact paths
and ancestry. Loose results keep their structure, so the two modes compose.

The Go package `tree` is the retained-model realisation. A port that offers no
retention is still conformant for reading; the round-trip requirement in the
conformance section does not apply to it.

## 5. Byte-supply contract

A conforming implementation offers a way to drive the cursor from a byte source
it owns. The source type is language-specific and deliberately unspecified;
`io.Reader` is Go's spelling of such a source.

The driver supplies bytes to the cursor for as long as the cursor reports
need-more-data, and never reports need-more-data to its own caller. When the
source is exhausted, the driver finalizes the input. A document that ended
inside an element is then reported as a structural truncation error, never as a
clean end of input. Omitting finalization would silently accept a truncated
document.

Supplying bytes does not invalidate the node the caller is holding. Finalizing
does invalidate it, just as acquiring the next event does; see section 3 for
the complete node and payload validity rule.

This contract does not replace the cursor's push-bytes/pull-events split. A
consumer that owns its own read loop drives the cursor directly and answers
need-more-data itself.

Because the driver has absorbed need-more-data, only two outcomes remain here --
a node, or the end of the input -- and this is the one layer where the host
language's iterator is an exact spelling rather than a lossy one. See
"Portability of API shape". A driver that offers an iterator SHOULD NOT also
offer a second, explicit acquire operation: the two spell the same pull, and the
redundant one is where the three-outcome collapse reappears.

> **Go mapping (non-normative):** `stream.Stream`, whose whole reading surface is
> `Nodes() iter.Seq2[parser.Node, error]` plus `Payload` and `Offset`. There is
> deliberately no exported `Next`. The end of the input ends the iteration instead
> of yielding a value; every other failure is yielded once, as the final pair, with
> a nil node, so a consumer cannot miss it by forgetting a separate `Err` call.
> Breaking out of the range leaves the stream standing on the node it broke on, and
> ranging again resumes after it.

## 6. Flow control

The consumer decides on the event it is holding, before requesting the next event
and before payload bytes need to be present:

* For a master, choose **descend** or **skip subtree**.
* For a leaf, choose **materialise payload** or **skip payload**.

Every event must offer exactly the operations that are valid for it, so that
requesting a leaf payload from a master, or deciding twice on one event, cannot be
expressed rather than merely being reported as an error.

Taking no decision at all is legal, and the defaults are the cheap ones: an
undecided master is descended into, and an undecided leaf has its payload skipped.
A consumer therefore materialises exactly the payloads it asks for, and it does not
pay to look at even those: delivered bytes may alias the reader's own buffer, so a
scan that reads every payload copies none of them. A payload request may report
need-more-data because the leaf header is available before its payload. The event
remains valid across additional input, so the consumer retries that same request
after feeding more data.

The cursor must retain only the state needed to continue the scan. In particular,
a consumer can skip bulk media without buffering it. The decision is made once,
on the header, and is carried out as later chunks arrive: a consumer is never
asked about the same element twice, and a payload whose bytes have not all arrived
reports need-more-data without invalidating the event, so the request is simply
retried after the next chunk. Therefore every chunking of the same byte stream
produces the same decisions and event sequence: the scan is split-invariant.

An optional boundary rule receives the open unknown-size master and the ID of the
next element. Returning true closes that master — its end event is reported first —
before the next element is reported at the enclosing depth. The rule belongs to the
cursor as a whole, not to an individual event: a per-event answer would let the same
stream be split differently depending on where the consumer happened to look.

## 7. Classification and registry

An implementation has no built-in element knowledge: the cursor owns no element
table and must not hard-code any element ID, not even the ones that frame every
EBML document. A classifier maps each element ID to an element kind and must
identify masters separately from leaves. The classifier is structural input to
the cursor; names, value types, aliases, and descriptions are not required for
parsing.

Supplying the classifier is therefore mandatory, not optional. A cursor is
constructed with one, and an implementation must not substitute a built-in
default when none is given: a default would classify an element the consumer's
registry does know as a leaf, so a master such as a `Cluster` would be read as
one opaque payload — a structural misreading that produces no error. A missing
classifier is a programmer error and must be rejected immediately and visibly at
construction (in Go: `parser.New` and `parser.NewCursor` take the classifier as
a required argument and panic on `nil`).

A registry is the separate source of element knowledge. It maps IDs to names and
value types, supplies the classifier, and can be extended with vendor or private
elements. An unknown ID is a readable binary leaf with its declared size. If it
is actually a master, its payload remains complete and can be parsed later; it
becomes a descended master only after the registry classifies it as one.

## 8. Required errors

An implementation must distinguish these behaviors:

* **Over-long VINT:** an element-ID VINT longer than 4 bytes or a size VINT
  longer than 8 bytes.
* **Truncated input at EOF:** EOF inside a header or inside a declared payload.
* **Child overflow:** a known-size child extent extends beyond its known-size
  parent master.
* **Unknown-size leaf:** a header declares unknown size but the classifier does
  not classify that ID as a master.
* **Premature explicit close:** a close-master request on a known-size master
  whose declared end has not been reached.

Insufficient input before EOF is not corruption; it is a request for more data.

### Error classification

Every error an implementation reports belongs to exactly one of three classes,
and a consumer must be able to tell them apart without inspecting messages or
enumerating concrete types.

* **Structural.** A failure of the cursor about the shape of the stream: the five
  required errors above, plus any other verdict that a cursor operation does not
  fit the current state. After one, the position of the next element is unknown,
  so the cursor is failed and stays failed. This is the only class after which a
  consumer may justifiably scan bytes forward for a resume point.
* **Content (consumer-originated).** A verdict a CONSUMER reached about what an
  element's bytes mean — a payload that will not decode, a value it refuses. The
  bytes were read correctly. Since a pull cursor never runs consumer code, the
  cursor cannot raise this error; the consumer raises it and passes it on, so the
  implementation must offer a wrapper that MARKS the class. It is not a statement
  about the stream's shape, so it must never be classified as structural and must
  never authorize byte scanning.
* **Need more data.** Not a failure. The cursor needs more input to decide, and
  the answer is the next chunk, or finalization once the input is over.

The two failure classes leave the stream in different states, and recovery
differs accordingly. After a structural failure the position of the next event
is unknown, so the only recovery is resynchronization: scanning forward for a
recognizable boundary and resuming there, discarding whatever lay between. A
content failure carries no such consequence, because the bytes were read
correctly and only their meaning was refused — the cursor's structural position
remains exactly where the failing element ends. An implementation MAY therefore
offer a consumer the option to skip just the offending element and continue from
that intact position, without scanning. This is optional consumer-facing policy,
not a requirement of this specification: a conforming implementation may instead
simply report the content failure and stop.

An implementation must expose a single, allocation-free test for each of the
first two classes, and the consumer's own error must remain reachable through the
wrapper that marks its class.

The structural test must be a **predicate that owns the class boundary at the
content wrapper**, not a mere membership check against a marker value:

* A content-originated failure is never classified as structural, **even when it
  carries a structural error value**. A consumer may pass on the implementation's
  own structural error, or anything wrapping it; the wrapper decides the class, and
  the inspection stops there.
* Need-more-data is not a failure and is never classified as structural.
* Every failure the cursor itself raises is classified as structural, however
  many layers a consumer has wrapped it in.
* Marking the class must not hide the consumer's own error: it stays reachable
  for a consumer inspecting the cause. Reachability and classification are
  separate questions, and only the classification stops at the boundary.

A marker value alone cannot satisfy this, because the idiomatic membership check
walks the entire causal chain and therefore cannot stop at the wrapper.

In Go: `parser.IsStructural(err)` is the canonical test — true for every
structural cursor failure, false for anything wrapped in `*parser.ContentError`
whatever it carries, and false for `NeedMoreData`. The `parser.ErrStructural`
sentinel remains so `errors.Is` keeps working on an error a cursor operation
returned directly, but it is not the classification test.
`parser.NewContentError(id, offset, err)` marks the content class,
`errors.As(err, &contentErr)` with a `*parser.ContentError` identifies it, and it
unwraps to the consumer's own error so `errors.Is`/`errors.As` reach its sentinels
and types unchanged.

## 9. Element checksums

EBML defines a CRC-32 element that carries a checksum of its parent master's
data. Reading a document does not require computing it, and an implementation
that never verifies a checksum is conformant: the element is a well-formed
binary leaf like any other, and a port may treat it as one. What this section
fixes is what the value MEANS, because that is where ports disagree silently.

1. **Coverage.** The checksum covers all the Element Data of the CRC-32
   element's PARENT master, AS STORED, minus the CRC-32 element itself — its
   header and its payload both. The parent's own header is not covered. The
   sibling elements' headers are covered, because they are part of the parent's
   data. A nested master's CRC-32 element is covered by its grandparent's
   checksum for the same reason.
2. **Algorithm.** The value is IEEE CRC-32, as in ISO 3309 and ITU-T V.42
   section 8.1.1.6.2, with an initial value of `0xFFFFFFFF`. It is computed on a
   little-endian bytestream.
3. **Storage.** The payload is exactly 4 bytes and holds the value in
   little-endian order. A CRC-32 element whose payload is any other length is a
   malformed element, and an implementation must not read a value out of it: a
   defect in the document must not be reported as a checksum disagreement about
   the parent's data, because the two have different causes and different
   remedies.
4. **Placement.** When present, the CRC-32 element is the FIRST ordered child of
   its parent master. A writer that emits one must emit it first; the value
   cannot be computed until the covered bytes exist, so a writer must have those
   bytes in hand before it emits the master's data.
5. **Classification.** A mismatch is a CONTENT failure in the sense of section 8,
   never a structural one. The extents were read correctly — the mismatch is
   discovered by summing bytes whose boundaries the cursor already established —
   so the position of the next element is known, the cursor is not failed, and a
   mismatch must never authorize byte scanning. Nothing about the parse is in
   doubt; only this element's bytes are.

Requirements 1 through 3 are the whole of the cross-language agreement, and each
half of a disagreement about them is invisible from inside. A port that covers
the parent's header, that includes the CRC-32 element in its own coverage, or
that stores the four bytes the other way round produces files that are entirely
self-consistent and that every other port reads as damaged — a mismatch on bytes
nothing ever damaged. A mismatch is also the one failure a reader cannot
investigate further, so what is summed must be unambiguous before any of it is
computed.

An implementation that offers verification must have the covered bytes, which
means retention: the covered definition is "as stored", and a cursor that hands
out a payload view valid only until the next event has nothing to sum. An
implementation that discarded a covered payload — by skipping a subtree, or under
a retention cap — must report that it cannot reach a verdict rather than
reporting either a pass or a mismatch. Silently passing an element that was never
checked is the worst of the three answers available, because the entire value of
a checksum is that a pass means something.

## 10. Conformance

The repository is the conformance corpus.

### Golden traces

Each `golden/**/*.jsonl` file contains one JSON object per cursor operation.
Fields are:

| Field | Meaning |
| --- | --- |
| `step` | One-based operation sequence number |
| `op` | Operation, such as `peek`, `consume`, `enter`, `skip`, or `leave` |
| `offset` | Absolute cursor offset at that operation |
| `depth` | Current enclosing-master depth |
| `id` | Lowercase hexadecimal element ID when the operation concerns an element |
| `size` | Declared payload size, with `-1` for unknown size |
| `kind` | Classified kind, or `end_master` for a close observation |
| `header_len` | Header length when an element header was observed |

Absent JSON fields mean that the operation has no value for that field.

### Fixtures and splits

Each `fixtures/**/*.ebml.hex` file is commented hexadecimal input. To recover
the input bytes: drop blank lines and lines beginning with `#` (descriptions),
split each remaining line on whitespace into tokens, concatenate every token
from every remaining line into one string in file order, and hex-decode that
string. A token is an arbitrary-length run of hexadecimal characters; token and
line boundaries carry no meaning of their own and do not need to align to byte
or element boundaries — some fixtures place one byte per token, others pack many
bytes per token.

`tests/split_patterns.json` defines the input chunkings. The committed patterns
are `one_byte` (one byte per chunk), `fibonacci` (Fibonacci-sized chunks), and
`random` (seed `12345`, maximum chunk size `7`).

An implementation conforms when it reproduces every committed golden JSONL file
byte-for-byte for every committed fixture under every split pattern.

### Round trip

For every committed fixture, a port that implements writing must parse the
fixture into its retained model and write it back byte-for-byte identically.
This requirement also asserts that the retained model is lossless, including
the original legal size-VINT width and the exact payload bytes. Reading-only
ports remain conformant without implementing this requirement.

The packages under `impl/go/ext/` are outside this contract. `scope`, `tags`, and
`fragment` are ways of using the core and are Go-only convenience layers whose
shape another language may choose differently. The retained element model is
specified above.
