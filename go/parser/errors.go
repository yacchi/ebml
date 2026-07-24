package parser

import (
	"errors"
	"fmt"
)

var (
	ErrElementIDTooLong       = errors.New("element ID VINT exceeds maximum length")
	ErrElementSizeTooLong     = errors.New("element size VINT exceeds maximum length")
	ErrTruncated              = errors.New("truncated input")
	ErrElementOverflowsParent = errors.New("element overflows parent")
)

type TruncatedError struct {
	Msg string
}

func (e TruncatedError) Error() string {
	if e.Msg == "" {
		return ErrTruncated.Error()
	}
	return fmt.Sprintf("%s: %s", ErrTruncated, e.Msg)
}

func (e TruncatedError) Unwrap() error {
	return ErrTruncated
}

type ElementOverflowError struct {
	ChildID   ElementID
	ChildEnd  int64
	ParentID  ElementID
	ParentEnd int64
}

func (e ElementOverflowError) Error() string {
	return fmt.Sprintf("element %s ends at offset %d beyond parent %s end offset %d",
		e.ChildID, e.ChildEnd, e.ParentID, e.ParentEnd)
}

func (e ElementOverflowError) Unwrap() error {
	return ErrElementOverflowsParent
}

type VINTLengthError struct {
	What   string
	Length int
	Max    int
	Cause  error
}

func (e VINTLengthError) Error() string {
	return fmt.Sprintf("%s VINT length %d exceeds maximum %d", e.What, e.Length, e.Max)
}

func (e VINTLengthError) Unwrap() error {
	return e.Cause
}

type NeedMoreData struct {
	MinBytes int
}

func (e NeedMoreData) Error() string {
	return fmt.Sprintf("need more data: min_bytes=%d", e.MinBytes)
}

type Invalid struct {
	Msg string
}

func (e Invalid) Error() string {
	if e.Msg == "" {
		return "invalid"
	}
	return "invalid: " + e.Msg
}
