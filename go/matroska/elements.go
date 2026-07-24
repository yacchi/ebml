package matroska

import (
	"fmt"
	"sort"

	"github.com/yacchi/ebml-reader/parser"
)

// ValueType identifies the EBML value representation of a Matroska element.
type ValueType uint8

const (
	TypeMaster ValueType = iota
	TypeUint
	TypeInt
	TypeFloat
	TypeString
	TypeUTF8
	TypeDate
	TypeBinary
	// TypeBlock is a library-level refinement of the RFC 9559 "binary" type: the
	// RFC classifies SimpleBlock/Block as binary, and TypeBlock marks the subset
	// of binary payloads whose internal structure this library can decode via
	// parser.ParseSimpleBlock.
	TypeBlock
)

func (t ValueType) String() string {
	switch t {
	case TypeMaster:
		return "master"
	case TypeUint:
		return "uint"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeUTF8:
		return "utf-8"
	case TypeDate:
		return "date"
	case TypeBinary:
		return "binary"
	case TypeBlock:
		return "block"
	default:
		return fmt.Sprintf("ValueType(%d)", t)
	}
}

const (
	IDEBML                    parser.ElementID = 0x1A45DFA3
	IDEBMLVersion             parser.ElementID = 0x4286
	IDEBMLReadVersion         parser.ElementID = 0x42F7
	IDEBMLMaxIDLength         parser.ElementID = 0x42F2
	IDEBMLMaxSizeLength       parser.ElementID = 0x42F3
	IDDocType                 parser.ElementID = 0x4282
	IDDocTypeVersion          parser.ElementID = 0x4287
	IDDocTypeReadVersion      parser.ElementID = 0x4285
	IDCRC32                   parser.ElementID = 0xBF
	IDVoid                    parser.ElementID = 0xEC
	IDSegment                 parser.ElementID = 0x18538067
	IDSeekHead                parser.ElementID = 0x114D9B74
	IDSeek                    parser.ElementID = 0x4DBB
	IDSeekID                  parser.ElementID = 0x53AB
	IDSeekPosition            parser.ElementID = 0x53AC
	IDInfo                    parser.ElementID = 0x1549A966
	IDSegmentUUID             parser.ElementID = 0x73A4
	IDTimestampScale          parser.ElementID = 0x2AD7B1
	IDDuration                parser.ElementID = 0x4489
	IDDateUTC                 parser.ElementID = 0x4461
	IDTitle                   parser.ElementID = 0x7BA9
	IDMuxingApp               parser.ElementID = 0x4D80
	IDWritingApp              parser.ElementID = 0x5741
	IDTracks                  parser.ElementID = 0x1654AE6B
	IDTrackEntry              parser.ElementID = 0xAE
	IDTrackNumber             parser.ElementID = 0xD7
	IDTrackUID                parser.ElementID = 0x73C5
	IDTrackType               parser.ElementID = 0x83
	IDFlagLacing              parser.ElementID = 0x9C
	IDName                    parser.ElementID = 0x536E
	IDLanguage                parser.ElementID = 0x22B59C
	IDCodecID                 parser.ElementID = 0x86
	IDCodecPrivate            parser.ElementID = 0x63A2
	IDCodecName               parser.ElementID = 0x258688
	IDDefaultDuration         parser.ElementID = 0x23E383
	IDAudio                   parser.ElementID = 0xE1
	IDSamplingFrequency       parser.ElementID = 0xB5
	IDOutputSamplingFrequency parser.ElementID = 0x78B5
	IDChannels                parser.ElementID = 0x9F
	IDBitDepth                parser.ElementID = 0x6264
	IDVideo                   parser.ElementID = 0xE0
	IDPixelWidth              parser.ElementID = 0xB0
	IDPixelHeight             parser.ElementID = 0xBA
	IDCluster                 parser.ElementID = 0x1F43B675
	IDTimestamp               parser.ElementID = 0xE7
	IDPosition                parser.ElementID = 0xA7
	IDPrevSize                parser.ElementID = 0xAB
	IDSimpleBlock             parser.ElementID = 0xA3
	IDBlockGroup              parser.ElementID = 0xA0
	IDBlock                   parser.ElementID = 0xA1
	IDBlockDuration           parser.ElementID = 0x9B
	IDReferenceBlock          parser.ElementID = 0xFB
	IDCues                    parser.ElementID = 0x1C53BB6B
	IDCuePoint                parser.ElementID = 0xBB
	IDCueTime                 parser.ElementID = 0xB3
	IDCueTrackPositions       parser.ElementID = 0xB7
	IDCueTrack                parser.ElementID = 0xF7
	IDCueClusterPosition      parser.ElementID = 0xF1
	IDTags                    parser.ElementID = 0x1254C367
	IDTag                     parser.ElementID = 0x7373
	IDTargets                 parser.ElementID = 0x63C0
	IDTargetTypeValue         parser.ElementID = 0x68CA
	IDTagTrackUID             parser.ElementID = 0x63C5
	IDSimpleTag               parser.ElementID = 0x67C8
	IDTagName                 parser.ElementID = 0x45A3
	IDTagLanguage             parser.ElementID = 0x447A
	IDTagDefault              parser.ElementID = 0x4484
	IDTagString               parser.ElementID = 0x4487
	IDTagBinary               parser.ElementID = 0x4485
	IDChapters                parser.ElementID = 0x1043A770
	IDEditionEntry            parser.ElementID = 0x45B9
	IDChapterAtom             parser.ElementID = 0xB6
	IDChapterTimeStart        parser.ElementID = 0x91
	IDChapterDisplay          parser.ElementID = 0x80
	IDChapString              parser.ElementID = 0x85
	IDAttachments             parser.ElementID = 0x1941A469
	IDAttachedFile            parser.ElementID = 0x61A7
	IDFileName                parser.ElementID = 0x466E
	IDFileMediaType           parser.ElementID = 0x4660
	IDFileData                parser.ElementID = 0x465C
	IDFileUID                 parser.ElementID = 0x46AE
	IDFileDescription         parser.ElementID = 0x467E
)

// ElementInfo describes a registered EBML/Matroska element.
type ElementInfo struct {
	ID   parser.ElementID
	Name string
	Type ValueType
}

var elements = map[parser.ElementID]ElementInfo{
	IDEBML:                    {IDEBML, "EBML", TypeMaster},
	IDEBMLVersion:             {IDEBMLVersion, "EBMLVersion", TypeUint},
	IDEBMLReadVersion:         {IDEBMLReadVersion, "EBMLReadVersion", TypeUint},
	IDEBMLMaxIDLength:         {IDEBMLMaxIDLength, "EBMLMaxIDLength", TypeUint},
	IDEBMLMaxSizeLength:       {IDEBMLMaxSizeLength, "EBMLMaxSizeLength", TypeUint},
	IDDocType:                 {IDDocType, "DocType", TypeString},
	IDDocTypeVersion:          {IDDocTypeVersion, "DocTypeVersion", TypeUint},
	IDDocTypeReadVersion:      {IDDocTypeReadVersion, "DocTypeReadVersion", TypeUint},
	IDCRC32:                   {IDCRC32, "CRC-32", TypeBinary},
	IDVoid:                    {IDVoid, "Void", TypeBinary},
	IDSegment:                 {IDSegment, "Segment", TypeMaster},
	IDSeekHead:                {IDSeekHead, "SeekHead", TypeMaster},
	IDSeek:                    {IDSeek, "Seek", TypeMaster},
	IDSeekID:                  {IDSeekID, "SeekID", TypeBinary},
	IDSeekPosition:            {IDSeekPosition, "SeekPosition", TypeUint},
	IDInfo:                    {IDInfo, "Info", TypeMaster},
	IDSegmentUUID:             {IDSegmentUUID, "SegmentUUID", TypeBinary},
	IDTimestampScale:          {IDTimestampScale, "TimestampScale", TypeUint},
	IDDuration:                {IDDuration, "Duration", TypeFloat},
	IDDateUTC:                 {IDDateUTC, "DateUTC", TypeDate},
	IDTitle:                   {IDTitle, "Title", TypeUTF8},
	IDMuxingApp:               {IDMuxingApp, "MuxingApp", TypeUTF8},
	IDWritingApp:              {IDWritingApp, "WritingApp", TypeUTF8},
	IDTracks:                  {IDTracks, "Tracks", TypeMaster},
	IDTrackEntry:              {IDTrackEntry, "TrackEntry", TypeMaster},
	IDTrackNumber:             {IDTrackNumber, "TrackNumber", TypeUint},
	IDTrackUID:                {IDTrackUID, "TrackUID", TypeUint},
	IDTrackType:               {IDTrackType, "TrackType", TypeUint},
	IDFlagLacing:              {IDFlagLacing, "FlagLacing", TypeUint},
	IDName:                    {IDName, "Name", TypeUTF8},
	IDLanguage:                {IDLanguage, "Language", TypeString},
	IDCodecID:                 {IDCodecID, "CodecID", TypeString},
	IDCodecPrivate:            {IDCodecPrivate, "CodecPrivate", TypeBinary},
	IDCodecName:               {IDCodecName, "CodecName", TypeUTF8},
	IDDefaultDuration:         {IDDefaultDuration, "DefaultDuration", TypeUint},
	IDAudio:                   {IDAudio, "Audio", TypeMaster},
	IDSamplingFrequency:       {IDSamplingFrequency, "SamplingFrequency", TypeFloat},
	IDOutputSamplingFrequency: {IDOutputSamplingFrequency, "OutputSamplingFrequency", TypeFloat},
	IDChannels:                {IDChannels, "Channels", TypeUint},
	IDBitDepth:                {IDBitDepth, "BitDepth", TypeUint},
	IDVideo:                   {IDVideo, "Video", TypeMaster},
	IDPixelWidth:              {IDPixelWidth, "PixelWidth", TypeUint},
	IDPixelHeight:             {IDPixelHeight, "PixelHeight", TypeUint},
	IDCluster:                 {IDCluster, "Cluster", TypeMaster},
	IDTimestamp:               {IDTimestamp, "Timestamp", TypeUint},
	IDPosition:                {IDPosition, "Position", TypeUint},
	IDPrevSize:                {IDPrevSize, "PrevSize", TypeUint},
	// SimpleBlock: RFC 9559 type is binary; TypeBlock is a library-level
	// refinement so callers can decode the payload with parser.ParseSimpleBlock.
	IDSimpleBlock: {IDSimpleBlock, "SimpleBlock", TypeBlock},
	IDBlockGroup:  {IDBlockGroup, "BlockGroup", TypeMaster},
	// Block: RFC 9559 type is binary; TypeBlock is a library-level refinement so
	// callers can decode the payload with parser.ParseSimpleBlock.
	IDBlock:              {IDBlock, "Block", TypeBlock},
	IDBlockDuration:      {IDBlockDuration, "BlockDuration", TypeUint},
	IDReferenceBlock:     {IDReferenceBlock, "ReferenceBlock", TypeInt},
	IDCues:               {IDCues, "Cues", TypeMaster},
	IDCuePoint:           {IDCuePoint, "CuePoint", TypeMaster},
	IDCueTime:            {IDCueTime, "CueTime", TypeUint},
	IDCueTrackPositions:  {IDCueTrackPositions, "CueTrackPositions", TypeMaster},
	IDCueTrack:           {IDCueTrack, "CueTrack", TypeUint},
	IDCueClusterPosition: {IDCueClusterPosition, "CueClusterPosition", TypeUint},
	IDTags:               {IDTags, "Tags", TypeMaster},
	IDTag:                {IDTag, "Tag", TypeMaster},
	IDTargets:            {IDTargets, "Targets", TypeMaster},
	IDTargetTypeValue:    {IDTargetTypeValue, "TargetTypeValue", TypeUint},
	IDTagTrackUID:        {IDTagTrackUID, "TagTrackUID", TypeUint},
	IDSimpleTag:          {IDSimpleTag, "SimpleTag", TypeMaster},
	IDTagName:            {IDTagName, "TagName", TypeUTF8},
	IDTagLanguage:        {IDTagLanguage, "TagLanguage", TypeString},
	IDTagDefault:         {IDTagDefault, "TagDefault", TypeUint},
	IDTagString:          {IDTagString, "TagString", TypeUTF8},
	IDTagBinary:          {IDTagBinary, "TagBinary", TypeBinary},
	IDChapters:           {IDChapters, "Chapters", TypeMaster},
	IDEditionEntry:       {IDEditionEntry, "EditionEntry", TypeMaster},
	IDChapterAtom:        {IDChapterAtom, "ChapterAtom", TypeMaster},
	IDChapterTimeStart:   {IDChapterTimeStart, "ChapterTimeStart", TypeUint},
	IDChapterDisplay:     {IDChapterDisplay, "ChapterDisplay", TypeMaster},
	IDChapString:         {IDChapString, "ChapString", TypeUTF8},
	IDAttachments:        {IDAttachments, "Attachments", TypeMaster},
	IDAttachedFile:       {IDAttachedFile, "AttachedFile", TypeMaster},
	IDFileName:           {IDFileName, "FileName", TypeUTF8},
	IDFileMediaType:      {IDFileMediaType, "FileMediaType", TypeString},
	IDFileData:           {IDFileData, "FileData", TypeBinary},
	IDFileUID:            {IDFileUID, "FileUID", TypeUint},
	IDFileDescription:    {IDFileDescription, "FileDescription", TypeUTF8},
}

var names = func() map[string]parser.ElementID {
	result := make(map[string]parser.ElementID, len(elements))
	for id, info := range elements {
		result[info.Name] = id
	}
	return result
}()

// legacyNames maps well-known pre-RFC 9559 element names to the ID of the
// element the RFC renamed them to. It is a compatibility shim for IDForName
// only: these names are never reported by Elements, Lookup, NameForID or
// Describe, which always use the canonical RFC 9559 name.
var legacyNames = map[string]parser.ElementID{
	"SegmentUID":    IDSegmentUUID,   // RFC 9559 renamed SegmentUID to SegmentUUID.
	"FileMimeType":  IDFileMediaType, // RFC 9559 renamed FileMimeType to FileMediaType.
	"Timecode":      IDTimestamp,     // RFC 9559 Section 11 renamed Timecode to Timestamp.
	"TimecodeScale": IDTimestampScale,
}

// Lookup returns the registry entry for id.
func Lookup(id parser.ElementID) (ElementInfo, bool) {
	info, ok := elements[id]
	return info, ok
}

// IDForName returns the registered ID for an exact element name.
//
// Primary names are the RFC 9559 ones, and they are matched first. Well-known
// pre-RFC names (for example "SegmentUID" for SegmentUUID, or "FileMimeType"
// for FileMediaType) are accepted as aliases via a fallback lookup, so callers
// written against the older matroska.org names still resolve. Aliases resolve
// to the same ID but are never returned as an element's name.
func IDForName(name string) (parser.ElementID, bool) {
	if id, ok := names[name]; ok {
		return id, true
	}
	id, ok := legacyNames[name]
	return id, ok
}

// Elements returns all registered elements sorted by ID.
func Elements() []ElementInfo {
	result := make([]ElementInfo, 0, len(elements))
	for _, info := range elements {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// TypeFor returns the registered value type for id.
func TypeFor(id parser.ElementID) (ValueType, bool) {
	info, ok := Lookup(id)
	if !ok {
		return 0, false
	}
	return info.Type, true
}

// Describe returns the registered name and ID, or just the ID if unknown.
func Describe(id parser.ElementID) string {
	if name := NameForID(id); name != "" {
		return fmt.Sprintf("%s (%s)", name, id)
	}
	return id.String()
}

// NameForID returns the registered name, or an empty string for an unknown ID.
func NameForID(id parser.ElementID) string {
	info, ok := Lookup(id)
	if !ok {
		return ""
	}
	return info.Name
}
