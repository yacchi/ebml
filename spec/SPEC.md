# SPEC — Streaming EBML Cursor Parser (MVP)

## 1. Terminology
- **offset**: Absolute byte position from the beginning of the input (0-based).
- **element header**: `id` (VINT) + `size` (VINT).
- **payload**: `size` bytes immediately following the header (for a master, a sequence of child elements).
- **unknown-size**: When the value of the size VINT has all bits set to 1 (represented as `size = -1` in this MVP).

## 2. API (conceptual)
- `feed(bytes)`: Append input (append-only).
- `peek()`: Return the next observable "event" (no side effects).
  - **Priority**: If a known-size master has reached its end, return `EndMaster`.
  - Otherwise, parse and return the element header.
- `consume_header()`: Consume the header of the element returned by the most recent `peek()` and advance the cursor to the start of its payload.
- `enter_master()`: If the most recently consumed element is a master, push it onto the master stack.
- `leave_master()`: Pop when `peek()` returns `EndMaster` (normally do not pop an unknown-size master).
- `skip_payload()`: Skip the payload of the most recently consumed element by its `size` (not used for masters).
- `finalize_eof()`: At end of input (EOF), close all remaining unknown-size masters (pop them from the stack).

## 3. Errors
- `NeedMoreData(min_bytes)`:
  - `peek()`: There is not enough data to determine the header's `id` or `size`.
  - `skip_payload()`: There is not enough data to skip the payload.
  - `min_bytes` returns the minimum number of additional bytes required from the current position, when it can be determined.
- `Invalid`: Invalid VINT length (`id>4`, `size>8`, etc.) or an operation that does not allow unknown-size values (for example, an unknown-size payload on a non-master).

## 4. Fixed decisions in the specification (resolving ambiguities)
- `peek()` has **no side effects**.
- The end of a known-size master is observed by having `peek()` return `EndMaster`.
- An unknown-size master is **closed at EOF** (`finalize_eof()` can generate a leave event).
- An unknown element ID has kind `binary` and can be skipped with `skip_payload()` (but its size must be known).
