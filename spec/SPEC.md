# Streaming EBML Cursor Specification

This document specifies the portable core of `ebml-reader`. It is a behavioral
contract for incremental EBML readers; it is not a document-model or retention
API. The core consists of a cursor with events and flow control, plus a separate
element registry and classifier.

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

Boundary detection must never scan payload bytes for the EBML header magic.
Payload data may contain that byte sequence.

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
reports a terminal structural or handler error remains failed. The two are
distinct classes a consumer must be able to separate; see "Error classification"
in section 6.

## 3. Event model

The event-driven interface reports a master or leaf on its header, optionally a
leaf payload after the payload has arrived, and a close for every master that
was entered. A skipped master has no descendant events and no close event.

Each element event carries:

| Field | Meaning |
| --- | --- |
| element ID | Complete encoded ID VINT, including marker bits |
| kind | Classifier result: `master`, `uint`, or `binary` for an element header; `end_master` for a close observation; `unknown` if the classifier does not recognize the element ID |
| depth | Number of enclosing entered masters; top-level is zero |
| header start offset | Absolute offset of the first header byte |
| header length | Encoded ID length plus encoded size length |
| declared size | Payload length, or unknown size |
| end offset | One past the declared element extent when known; unknown until a master closes when size is unknown |
| enclosing-master chain | IDs of enclosing entered masters, outermost first |

A close event carries the closed master's identity, depth, header location,
declared size, enclosing-master chain, and the concrete offset at which it
closed. For a known-size master this is observable immediately when its declared
payload bytes have been consumed, regardless of any enclosing unknown-size
master.

## 4. Flow control

The handler decides on the header, before payload bytes need to be present:

* For a master, choose **descend** or **skip subtree**.
* For a leaf, choose **deliver payload** or **skip payload**.

The cursor must retain only the state needed to continue the scan. In particular,
a consumer can skip bulk media without buffering it. The decision is made once,
on the header, and is carried out as later chunks arrive. Therefore every
chunking of the same byte stream produces the same decisions and event sequence:
the scan is split-invariant.

An optional boundary decider receives the open unknown-size master and the next
element header. Returning true closes that master before the next element is
re-examined at the enclosing depth.

## 5. Classification and registry

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
construction (in Go: `parser.New` and `parser.NewScanner` take the classifier as
a required argument and panic on `nil`).

A registry is the separate source of element knowledge. It maps IDs to names and
value types, supplies the classifier, and can be extended with vendor or private
elements. An unknown ID is a readable binary leaf with its declared size. If it
is actually a master, its payload remains complete and can be parsed later; it
becomes a descended master only after the registry classifies it as one.

## 6. Required errors

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
* **Handler-originated.** An error returned by a consumer's own handler from an
  element, payload, or close event. The bytes were read correctly; the consumer
  refused their content. It aborts the scan and is reported to the caller
  unchanged, but it is not a statement about the stream's shape, so it must never
  be classified as structural and must never authorize byte scanning.
* **Need more data.** Not a failure. The cursor needs more input to decide, and
  the answer is the next chunk. It is excluded from both classes above, and the
  event-driven interface absorbs it rather than reporting it.

An implementation must expose a single, allocation-free test for each of the
first two classes, and the handler's own error must remain reachable through the
wrapper that marks its origin.

The structural test must be a **predicate that owns the class boundary at the
handler**, not a mere membership check against a marker value:

* A handler-originated failure is never classified as structural, **even when it
  carries a structural error value**. A handler may return the implementation's
  own structural error, or anything wrapping it; the origin wrapper decides the
  class, and the inspection stops there.
* Need-more-data is not a failure and is never classified as structural.
* Every failure the cursor itself raises is classified as structural, however
  many layers a consumer has wrapped it in.
* Marking the origin must not hide the handler's own error: it stays reachable
  for a consumer inspecting the cause. Reachability and classification are
  separate questions, and only the classification stops at the boundary.

A marker value alone cannot satisfy this, because the idiomatic membership check
walks the entire causal chain and therefore cannot stop at the handler.

In Go: `parser.IsStructural(err)` is the canonical test — true for every
structural cursor failure, false for anything wrapped in `*parser.HandlerError`
whatever it carries, and false for `NeedMoreData`. The `parser.ErrStructural`
sentinel remains so `errors.Is` keeps working on an error a cursor operation
returned directly, but it is not the classification test.
`errors.As(err, &handlerErr)` with a `*parser.HandlerError` identifies the
handler-originated class, records which event failed, and unwraps to the
handler's own error so `errors.Is`/`errors.As` reach its sentinels and types
unchanged.

## 7. Conformance

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

The packages under `go/ext/` are outside this contract. The retained tree and
the per-Cluster assembler are Go-only convenience layers and are not required
of another-language implementation.
