package matroska

import (
	"fmt"

	"github.com/yacchi/ebml/parser"
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
	// TypeUnknown is never stored in the registry: it is what Element.Type
	// reports for an element ID the registry does not know, so callers can tell
	// "this library has no type information for this ID" apart from a genuine
	// TypeMaster (which is the zero ValueType) or TypeBinary entry. Elements with
	// an unknown ID are still fully readable through the Element accessors.
	TypeUnknown
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
	case TypeUnknown:
		return "unknown"
	default:
		return fmt.Sprintf("ValueType(%d)", t)
	}
}

// Element IDs, in document order: the EBML header, then the Segment's
// Top-Level Elements and their subtrees. The order matches the official EBML
// Schema so the two can be read side by side; internal/specconform checks every
// ID, name and value type below against that schema.
//
// Elements RFC 9559 marks as deprecated are registered too. Naming one costs
// nothing and makes an old file readable rather than opaque -- with two
// deliberate exceptions, SilentTracks and EncryptedBlock, which are Cluster
// children and are documented at completeChildren below.
const (
	// EBML header (RFC 8794).
	IDEBML                    parser.ElementID = 0x1A45DFA3
	IDEBMLVersion             parser.ElementID = 0x4286
	IDEBMLReadVersion         parser.ElementID = 0x42F7
	IDEBMLMaxIDLength         parser.ElementID = 0x42F2
	IDEBMLMaxSizeLength       parser.ElementID = 0x42F3
	IDDocType                 parser.ElementID = 0x4282
	IDDocTypeVersion          parser.ElementID = 0x4287
	IDDocTypeReadVersion      parser.ElementID = 0x4285
	IDDocTypeExtension        parser.ElementID = 0x4281
	IDDocTypeExtensionName    parser.ElementID = 0x4283
	IDDocTypeExtensionVersion parser.ElementID = 0x4284

	// Global elements: legal inside any master, at any depth.
	IDCRC32 parser.ElementID = 0xBF
	IDVoid  parser.ElementID = 0xEC

	// Segment and SeekHead.
	IDSegment      parser.ElementID = 0x18538067
	IDSeekHead     parser.ElementID = 0x114D9B74
	IDSeek         parser.ElementID = 0x4DBB
	IDSeekID       parser.ElementID = 0x53AB
	IDSeekPosition parser.ElementID = 0x53AC

	// Info.
	IDInfo                       parser.ElementID = 0x1549A966
	IDSegmentUUID                parser.ElementID = 0x73A4
	IDSegmentFilename            parser.ElementID = 0x7384
	IDPrevUUID                   parser.ElementID = 0x3CB923
	IDPrevFilename               parser.ElementID = 0x3C83AB
	IDNextUUID                   parser.ElementID = 0x3EB923
	IDNextFilename               parser.ElementID = 0x3E83BB
	IDSegmentFamily              parser.ElementID = 0x4444
	IDChapterTranslate           parser.ElementID = 0x6924
	IDChapterTranslateID         parser.ElementID = 0x69A5
	IDChapterTranslateCodec      parser.ElementID = 0x69BF
	IDChapterTranslateEditionUID parser.ElementID = 0x69FC
	IDTimestampScale             parser.ElementID = 0x2AD7B1
	IDDuration                   parser.ElementID = 0x4489
	IDDateUTC                    parser.ElementID = 0x4461
	IDTitle                      parser.ElementID = 0x7BA9
	IDMuxingApp                  parser.ElementID = 0x4D80
	IDWritingApp                 parser.ElementID = 0x5741

	// Tracks and TrackEntry.
	IDTracks                      parser.ElementID = 0x1654AE6B
	IDTrackEntry                  parser.ElementID = 0xAE
	IDTrackNumber                 parser.ElementID = 0xD7
	IDTrackUID                    parser.ElementID = 0x73C5
	IDTrackType                   parser.ElementID = 0x83
	IDFlagEnabled                 parser.ElementID = 0xB9
	IDFlagDefault                 parser.ElementID = 0x88
	IDFlagForced                  parser.ElementID = 0x55AA
	IDFlagHearingImpaired         parser.ElementID = 0x55AB
	IDFlagVisualImpaired          parser.ElementID = 0x55AC
	IDFlagTextDescriptions        parser.ElementID = 0x55AD
	IDFlagOriginal                parser.ElementID = 0x55AE
	IDFlagCommentary              parser.ElementID = 0x55AF
	IDFlagLacing                  parser.ElementID = 0x9C
	IDMinCache                    parser.ElementID = 0x6DE7
	IDMaxCache                    parser.ElementID = 0x6DF8
	IDDefaultDuration             parser.ElementID = 0x23E383
	IDDefaultDecodedFieldDuration parser.ElementID = 0x234E7A
	IDTrackTimestampScale         parser.ElementID = 0x23314F
	IDTrackOffset                 parser.ElementID = 0x537F
	IDMaxBlockAdditionID          parser.ElementID = 0x55EE
	IDBlockAdditionMapping        parser.ElementID = 0x41E4
	IDBlockAddIDValue             parser.ElementID = 0x41F0
	IDBlockAddIDName              parser.ElementID = 0x41A4
	IDBlockAddIDType              parser.ElementID = 0x41E7
	IDBlockAddIDExtraData         parser.ElementID = 0x41ED
	IDName                        parser.ElementID = 0x536E
	IDLanguage                    parser.ElementID = 0x22B59C
	IDLanguageBCP47               parser.ElementID = 0x22B59D
	IDCodecID                     parser.ElementID = 0x86
	IDCodecPrivate                parser.ElementID = 0x63A2
	IDCodecName                   parser.ElementID = 0x258688
	IDAttachmentLink              parser.ElementID = 0x7446
	IDCodecSettings               parser.ElementID = 0x3A9697
	IDCodecInfoURL                parser.ElementID = 0x3B4040
	IDCodecDownloadURL            parser.ElementID = 0x26B240
	IDCodecDecodeAll              parser.ElementID = 0xAA
	IDTrackOverlay                parser.ElementID = 0x6FAB
	IDCodecDelay                  parser.ElementID = 0x56AA
	IDSeekPreRoll                 parser.ElementID = 0x56BB
	IDTrackTranslate              parser.ElementID = 0x6624
	IDTrackTranslateTrackID       parser.ElementID = 0x66A5
	IDTrackTranslateCodec         parser.ElementID = 0x66BF
	IDTrackTranslateEditionUID    parser.ElementID = 0x66FC
	IDTrickTrackUID               parser.ElementID = 0xC0
	IDTrickTrackSegmentUID        parser.ElementID = 0xC1
	IDTrickTrackFlag              parser.ElementID = 0xC6
	IDTrickMasterTrackUID         parser.ElementID = 0xC7
	IDTrickMasterTrackSegmentUID  parser.ElementID = 0xC4
	IDTrackOperation              parser.ElementID = 0xE2
	IDTrackCombinePlanes          parser.ElementID = 0xE3
	IDTrackPlane                  parser.ElementID = 0xE4
	IDTrackPlaneUID               parser.ElementID = 0xE5
	IDTrackPlaneType              parser.ElementID = 0xE6
	IDTrackJoinBlocks             parser.ElementID = 0xE9
	IDTrackJoinUID                parser.ElementID = 0xED

	// TrackEntry\Video, including Colour and Projection.
	IDVideo                   parser.ElementID = 0xE0
	IDFlagInterlaced          parser.ElementID = 0x9A
	IDFieldOrder              parser.ElementID = 0x9D
	IDStereoMode              parser.ElementID = 0x53B8
	IDAlphaMode               parser.ElementID = 0x53C0
	IDOldStereoMode           parser.ElementID = 0x53B9
	IDPixelWidth              parser.ElementID = 0xB0
	IDPixelHeight             parser.ElementID = 0xBA
	IDPixelCropBottom         parser.ElementID = 0x54AA
	IDPixelCropTop            parser.ElementID = 0x54BB
	IDPixelCropLeft           parser.ElementID = 0x54CC
	IDPixelCropRight          parser.ElementID = 0x54DD
	IDDisplayWidth            parser.ElementID = 0x54B0
	IDDisplayHeight           parser.ElementID = 0x54BA
	IDDisplayUnit             parser.ElementID = 0x54B2
	IDAspectRatioType         parser.ElementID = 0x54B3
	IDUncompressedFourCC      parser.ElementID = 0x2EB524
	IDGammaValue              parser.ElementID = 0x2FB523
	IDFrameRate               parser.ElementID = 0x2383E3
	IDColour                  parser.ElementID = 0x55B0
	IDMatrixCoefficients      parser.ElementID = 0x55B1
	IDBitsPerChannel          parser.ElementID = 0x55B2
	IDChromaSubsamplingHorz   parser.ElementID = 0x55B3
	IDChromaSubsamplingVert   parser.ElementID = 0x55B4
	IDCbSubsamplingHorz       parser.ElementID = 0x55B5
	IDCbSubsamplingVert       parser.ElementID = 0x55B6
	IDChromaSitingHorz        parser.ElementID = 0x55B7
	IDChromaSitingVert        parser.ElementID = 0x55B8
	IDRange                   parser.ElementID = 0x55B9
	IDTransferCharacteristics parser.ElementID = 0x55BA
	IDPrimaries               parser.ElementID = 0x55BB
	IDMaxCLL                  parser.ElementID = 0x55BC
	IDMaxFALL                 parser.ElementID = 0x55BD
	IDMasteringMetadata       parser.ElementID = 0x55D0
	IDPrimaryRChromaticityX   parser.ElementID = 0x55D1
	IDPrimaryRChromaticityY   parser.ElementID = 0x55D2
	IDPrimaryGChromaticityX   parser.ElementID = 0x55D3
	IDPrimaryGChromaticityY   parser.ElementID = 0x55D4
	IDPrimaryBChromaticityX   parser.ElementID = 0x55D5
	IDPrimaryBChromaticityY   parser.ElementID = 0x55D6
	IDWhitePointChromaticityX parser.ElementID = 0x55D7
	IDWhitePointChromaticityY parser.ElementID = 0x55D8
	IDLuminanceMax            parser.ElementID = 0x55D9
	IDLuminanceMin            parser.ElementID = 0x55DA
	IDProjection              parser.ElementID = 0x7670
	IDProjectionType          parser.ElementID = 0x7671
	IDProjectionPrivate       parser.ElementID = 0x7672
	IDProjectionPoseYaw       parser.ElementID = 0x7673
	IDProjectionPosePitch     parser.ElementID = 0x7674
	IDProjectionPoseRoll      parser.ElementID = 0x7675

	// TrackEntry\Audio.
	IDAudio                   parser.ElementID = 0xE1
	IDSamplingFrequency       parser.ElementID = 0xB5
	IDOutputSamplingFrequency parser.ElementID = 0x78B5
	IDChannels                parser.ElementID = 0x9F
	IDChannelPositions        parser.ElementID = 0x7D7B
	IDBitDepth                parser.ElementID = 0x6264
	IDEmphasis                parser.ElementID = 0x52F1

	// TrackEntry\ContentEncodings.
	IDContentEncodings      parser.ElementID = 0x6D80
	IDContentEncoding       parser.ElementID = 0x6240
	IDContentEncodingOrder  parser.ElementID = 0x5031
	IDContentEncodingScope  parser.ElementID = 0x5032
	IDContentEncodingType   parser.ElementID = 0x5033
	IDContentCompression    parser.ElementID = 0x5034
	IDContentCompAlgo       parser.ElementID = 0x4254
	IDContentCompSettings   parser.ElementID = 0x4255
	IDContentEncryption     parser.ElementID = 0x5035
	IDContentEncAlgo        parser.ElementID = 0x47E1
	IDContentEncKeyID       parser.ElementID = 0x47E2
	IDContentEncAESSettings parser.ElementID = 0x47E7
	IDAESSettingsCipherMode parser.ElementID = 0x47E8
	IDContentSignature      parser.ElementID = 0x47E3
	IDContentSigKeyID       parser.ElementID = 0x47E4
	IDContentSigAlgo        parser.ElementID = 0x47E5
	IDContentSigHashAlgo    parser.ElementID = 0x47E6

	// Cluster and BlockGroup.
	IDCluster            parser.ElementID = 0x1F43B675
	IDTimestamp          parser.ElementID = 0xE7
	IDPosition           parser.ElementID = 0xA7
	IDPrevSize           parser.ElementID = 0xAB
	IDSimpleBlock        parser.ElementID = 0xA3
	IDBlockGroup         parser.ElementID = 0xA0
	IDBlock              parser.ElementID = 0xA1
	IDBlockVirtual       parser.ElementID = 0xA2
	IDBlockAdditions     parser.ElementID = 0x75A1
	IDBlockMore          parser.ElementID = 0xA6
	IDBlockAddID         parser.ElementID = 0xEE
	IDBlockAdditional    parser.ElementID = 0xA5
	IDBlockDuration      parser.ElementID = 0x9B
	IDReferencePriority  parser.ElementID = 0xFA
	IDReferenceBlock     parser.ElementID = 0xFB
	IDReferenceVirtual   parser.ElementID = 0xFD
	IDCodecState         parser.ElementID = 0xA4
	IDDiscardPadding     parser.ElementID = 0x75A2
	IDSlices             parser.ElementID = 0x8E
	IDTimeSlice          parser.ElementID = 0xE8
	IDLaceNumber         parser.ElementID = 0xCC
	IDFrameNumber        parser.ElementID = 0xCD
	IDBlockAdditionID    parser.ElementID = 0xCB
	IDDelay              parser.ElementID = 0xCE
	IDSliceDuration      parser.ElementID = 0xCF
	IDReferenceFrame     parser.ElementID = 0xC8
	IDReferenceOffset    parser.ElementID = 0xC9
	IDReferenceTimestamp parser.ElementID = 0xCA

	// Cues.
	IDCues                parser.ElementID = 0x1C53BB6B
	IDCuePoint            parser.ElementID = 0xBB
	IDCueTime             parser.ElementID = 0xB3
	IDCueTrackPositions   parser.ElementID = 0xB7
	IDCueTrack            parser.ElementID = 0xF7
	IDCueClusterPosition  parser.ElementID = 0xF1
	IDCueRelativePosition parser.ElementID = 0xF0
	IDCueDuration         parser.ElementID = 0xB2
	IDCueBlockNumber      parser.ElementID = 0x5378
	IDCueCodecState       parser.ElementID = 0xEA
	IDCueReference        parser.ElementID = 0xDB
	IDCueRefTime          parser.ElementID = 0x96
	IDCueRefCluster       parser.ElementID = 0x97
	IDCueRefNumber        parser.ElementID = 0x535F
	IDCueRefCodecState    parser.ElementID = 0xEB

	// Tags.
	IDTags               parser.ElementID = 0x1254C367
	IDTag                parser.ElementID = 0x7373
	IDTargets            parser.ElementID = 0x63C0
	IDTargetTypeValue    parser.ElementID = 0x68CA
	IDTargetType         parser.ElementID = 0x63CA
	IDTagTrackUID        parser.ElementID = 0x63C5
	IDTagEditionUID      parser.ElementID = 0x63C9
	IDTagChapterUID      parser.ElementID = 0x63C4
	IDTagAttachmentUID   parser.ElementID = 0x63C6
	IDTagBlockAddIDValue parser.ElementID = 0x63C7
	IDSimpleTag          parser.ElementID = 0x67C8
	IDTagName            parser.ElementID = 0x45A3
	IDTagLanguage        parser.ElementID = 0x447A
	IDTagLanguageBCP47   parser.ElementID = 0x447B
	IDTagDefault         parser.ElementID = 0x4484
	IDTagDefaultBogus    parser.ElementID = 0x44B4
	IDTagString          parser.ElementID = 0x4487
	IDTagBinary          parser.ElementID = 0x4485

	// Chapters.
	IDChapters                 parser.ElementID = 0x1043A770
	IDEditionEntry             parser.ElementID = 0x45B9
	IDEditionUID               parser.ElementID = 0x45BC
	IDEditionFlagHidden        parser.ElementID = 0x45BD
	IDEditionFlagDefault       parser.ElementID = 0x45DB
	IDEditionFlagOrdered       parser.ElementID = 0x45DD
	IDEditionDisplay           parser.ElementID = 0x4520
	IDEditionString            parser.ElementID = 0x4521
	IDEditionLanguageIETF      parser.ElementID = 0x45E4
	IDChapterAtom              parser.ElementID = 0xB6
	IDChapterUID               parser.ElementID = 0x73C4
	IDChapterStringUID         parser.ElementID = 0x5654
	IDChapterTimeStart         parser.ElementID = 0x91
	IDChapterTimeEnd           parser.ElementID = 0x92
	IDChapterFlagHidden        parser.ElementID = 0x98
	IDChapterFlagEnabled       parser.ElementID = 0x4598
	IDChapterSegmentUUID       parser.ElementID = 0x6E67
	IDChapterSkipType          parser.ElementID = 0x4588
	IDChapterSegmentEditionUID parser.ElementID = 0x6EBC
	IDChapterPhysicalEquiv     parser.ElementID = 0x63C3
	IDChapterTrack             parser.ElementID = 0x8F
	IDChapterTrackUID          parser.ElementID = 0x89
	IDChapterDisplay           parser.ElementID = 0x80
	IDChapString               parser.ElementID = 0x85
	IDChapLanguage             parser.ElementID = 0x437C
	IDChapLanguageBCP47        parser.ElementID = 0x437D
	IDChapCountry              parser.ElementID = 0x437E
	IDChapProcess              parser.ElementID = 0x6944
	IDChapProcessCodecID       parser.ElementID = 0x6955
	IDChapProcessPrivate       parser.ElementID = 0x450D
	IDChapProcessCommand       parser.ElementID = 0x6911
	IDChapProcessTime          parser.ElementID = 0x6922
	IDChapProcessData          parser.ElementID = 0x6933

	// Attachments.
	IDAttachments       parser.ElementID = 0x1941A469
	IDAttachedFile      parser.ElementID = 0x61A7
	IDFileDescription   parser.ElementID = 0x467E
	IDFileName          parser.ElementID = 0x466E
	IDFileMediaType     parser.ElementID = 0x4660
	IDFileData          parser.ElementID = 0x465C
	IDFileUID           parser.ElementID = 0x46AE
	IDFileReferral      parser.ElementID = 0x4675
	IDFileUsedStartTime parser.ElementID = 0x4661
	IDFileUsedEndTime   parser.ElementID = 0x4662
)

// ElementInfo describes a registered EBML/Matroska element. It is also the entry
// a consumer passes to Registry.Register to teach this library a vendor or
// private element; Type TypeMaster is what makes the cursor descend into it.
type ElementInfo struct {
	ID   parser.ElementID
	Name string
	Type ValueType
}

// elements is the built-in RFC 9559 element table backing Default(). It is never
// mutated after initialization: the registry that exposes it is immutable.
//
// The value type is the schema's, with the single declared refinement noted at
// SimpleBlock and Block below.
var elements = map[parser.ElementID]ElementInfo{
	// EBML header.
	IDEBML:                    {IDEBML, "EBML", TypeMaster},
	IDEBMLVersion:             {IDEBMLVersion, "EBMLVersion", TypeUint},
	IDEBMLReadVersion:         {IDEBMLReadVersion, "EBMLReadVersion", TypeUint},
	IDEBMLMaxIDLength:         {IDEBMLMaxIDLength, "EBMLMaxIDLength", TypeUint},
	IDEBMLMaxSizeLength:       {IDEBMLMaxSizeLength, "EBMLMaxSizeLength", TypeUint},
	IDDocType:                 {IDDocType, "DocType", TypeString},
	IDDocTypeVersion:          {IDDocTypeVersion, "DocTypeVersion", TypeUint},
	IDDocTypeReadVersion:      {IDDocTypeReadVersion, "DocTypeReadVersion", TypeUint},
	IDDocTypeExtension:        {IDDocTypeExtension, "DocTypeExtension", TypeMaster},
	IDDocTypeExtensionName:    {IDDocTypeExtensionName, "DocTypeExtensionName", TypeString},
	IDDocTypeExtensionVersion: {IDDocTypeExtensionVersion, "DocTypeExtensionVersion", TypeUint},

	// Global elements.
	IDCRC32: {IDCRC32, "CRC-32", TypeBinary},
	IDVoid:  {IDVoid, "Void", TypeBinary},

	// Segment and SeekHead.
	IDSegment:      {IDSegment, "Segment", TypeMaster},
	IDSeekHead:     {IDSeekHead, "SeekHead", TypeMaster},
	IDSeek:         {IDSeek, "Seek", TypeMaster},
	IDSeekID:       {IDSeekID, "SeekID", TypeBinary},
	IDSeekPosition: {IDSeekPosition, "SeekPosition", TypeUint},

	// Info.
	IDInfo:                       {IDInfo, "Info", TypeMaster},
	IDSegmentUUID:                {IDSegmentUUID, "SegmentUUID", TypeBinary},
	IDSegmentFilename:            {IDSegmentFilename, "SegmentFilename", TypeUTF8},
	IDPrevUUID:                   {IDPrevUUID, "PrevUUID", TypeBinary},
	IDPrevFilename:               {IDPrevFilename, "PrevFilename", TypeUTF8},
	IDNextUUID:                   {IDNextUUID, "NextUUID", TypeBinary},
	IDNextFilename:               {IDNextFilename, "NextFilename", TypeUTF8},
	IDSegmentFamily:              {IDSegmentFamily, "SegmentFamily", TypeBinary},
	IDChapterTranslate:           {IDChapterTranslate, "ChapterTranslate", TypeMaster},
	IDChapterTranslateID:         {IDChapterTranslateID, "ChapterTranslateID", TypeBinary},
	IDChapterTranslateCodec:      {IDChapterTranslateCodec, "ChapterTranslateCodec", TypeUint},
	IDChapterTranslateEditionUID: {IDChapterTranslateEditionUID, "ChapterTranslateEditionUID", TypeUint},
	IDTimestampScale:             {IDTimestampScale, "TimestampScale", TypeUint},
	IDDuration:                   {IDDuration, "Duration", TypeFloat},
	IDDateUTC:                    {IDDateUTC, "DateUTC", TypeDate},
	IDTitle:                      {IDTitle, "Title", TypeUTF8},
	IDMuxingApp:                  {IDMuxingApp, "MuxingApp", TypeUTF8},
	IDWritingApp:                 {IDWritingApp, "WritingApp", TypeUTF8},

	// Tracks and TrackEntry.
	IDTracks:                      {IDTracks, "Tracks", TypeMaster},
	IDTrackEntry:                  {IDTrackEntry, "TrackEntry", TypeMaster},
	IDTrackNumber:                 {IDTrackNumber, "TrackNumber", TypeUint},
	IDTrackUID:                    {IDTrackUID, "TrackUID", TypeUint},
	IDTrackType:                   {IDTrackType, "TrackType", TypeUint},
	IDFlagEnabled:                 {IDFlagEnabled, "FlagEnabled", TypeUint},
	IDFlagDefault:                 {IDFlagDefault, "FlagDefault", TypeUint},
	IDFlagForced:                  {IDFlagForced, "FlagForced", TypeUint},
	IDFlagHearingImpaired:         {IDFlagHearingImpaired, "FlagHearingImpaired", TypeUint},
	IDFlagVisualImpaired:          {IDFlagVisualImpaired, "FlagVisualImpaired", TypeUint},
	IDFlagTextDescriptions:        {IDFlagTextDescriptions, "FlagTextDescriptions", TypeUint},
	IDFlagOriginal:                {IDFlagOriginal, "FlagOriginal", TypeUint},
	IDFlagCommentary:              {IDFlagCommentary, "FlagCommentary", TypeUint},
	IDFlagLacing:                  {IDFlagLacing, "FlagLacing", TypeUint},
	IDMinCache:                    {IDMinCache, "MinCache", TypeUint},
	IDMaxCache:                    {IDMaxCache, "MaxCache", TypeUint},
	IDDefaultDuration:             {IDDefaultDuration, "DefaultDuration", TypeUint},
	IDDefaultDecodedFieldDuration: {IDDefaultDecodedFieldDuration, "DefaultDecodedFieldDuration", TypeUint},
	IDTrackTimestampScale:         {IDTrackTimestampScale, "TrackTimestampScale", TypeFloat},
	IDTrackOffset:                 {IDTrackOffset, "TrackOffset", TypeInt},
	IDMaxBlockAdditionID:          {IDMaxBlockAdditionID, "MaxBlockAdditionID", TypeUint},
	IDBlockAdditionMapping:        {IDBlockAdditionMapping, "BlockAdditionMapping", TypeMaster},
	IDBlockAddIDValue:             {IDBlockAddIDValue, "BlockAddIDValue", TypeUint},
	IDBlockAddIDName:              {IDBlockAddIDName, "BlockAddIDName", TypeString},
	IDBlockAddIDType:              {IDBlockAddIDType, "BlockAddIDType", TypeUint},
	IDBlockAddIDExtraData:         {IDBlockAddIDExtraData, "BlockAddIDExtraData", TypeBinary},
	IDName:                        {IDName, "Name", TypeUTF8},
	IDLanguage:                    {IDLanguage, "Language", TypeString},
	IDLanguageBCP47:               {IDLanguageBCP47, "LanguageBCP47", TypeString},
	IDCodecID:                     {IDCodecID, "CodecID", TypeString},
	IDCodecPrivate:                {IDCodecPrivate, "CodecPrivate", TypeBinary},
	IDCodecName:                   {IDCodecName, "CodecName", TypeUTF8},
	IDAttachmentLink:              {IDAttachmentLink, "AttachmentLink", TypeUint},
	IDCodecSettings:               {IDCodecSettings, "CodecSettings", TypeUTF8},
	IDCodecInfoURL:                {IDCodecInfoURL, "CodecInfoURL", TypeString},
	IDCodecDownloadURL:            {IDCodecDownloadURL, "CodecDownloadURL", TypeString},
	IDCodecDecodeAll:              {IDCodecDecodeAll, "CodecDecodeAll", TypeUint},
	IDTrackOverlay:                {IDTrackOverlay, "TrackOverlay", TypeUint},
	IDCodecDelay:                  {IDCodecDelay, "CodecDelay", TypeUint},
	IDSeekPreRoll:                 {IDSeekPreRoll, "SeekPreRoll", TypeUint},
	IDTrackTranslate:              {IDTrackTranslate, "TrackTranslate", TypeMaster},
	IDTrackTranslateTrackID:       {IDTrackTranslateTrackID, "TrackTranslateTrackID", TypeBinary},
	IDTrackTranslateCodec:         {IDTrackTranslateCodec, "TrackTranslateCodec", TypeUint},
	IDTrackTranslateEditionUID:    {IDTrackTranslateEditionUID, "TrackTranslateEditionUID", TypeUint},
	IDTrickTrackUID:               {IDTrickTrackUID, "TrickTrackUID", TypeUint},
	IDTrickTrackSegmentUID:        {IDTrickTrackSegmentUID, "TrickTrackSegmentUID", TypeBinary},
	IDTrickTrackFlag:              {IDTrickTrackFlag, "TrickTrackFlag", TypeUint},
	IDTrickMasterTrackUID:         {IDTrickMasterTrackUID, "TrickMasterTrackUID", TypeUint},
	IDTrickMasterTrackSegmentUID:  {IDTrickMasterTrackSegmentUID, "TrickMasterTrackSegmentUID", TypeBinary},
	IDTrackOperation:              {IDTrackOperation, "TrackOperation", TypeMaster},
	IDTrackCombinePlanes:          {IDTrackCombinePlanes, "TrackCombinePlanes", TypeMaster},
	IDTrackPlane:                  {IDTrackPlane, "TrackPlane", TypeMaster},
	IDTrackPlaneUID:               {IDTrackPlaneUID, "TrackPlaneUID", TypeUint},
	IDTrackPlaneType:              {IDTrackPlaneType, "TrackPlaneType", TypeUint},
	IDTrackJoinBlocks:             {IDTrackJoinBlocks, "TrackJoinBlocks", TypeMaster},
	IDTrackJoinUID:                {IDTrackJoinUID, "TrackJoinUID", TypeUint},

	// TrackEntry\Video.
	IDVideo:                   {IDVideo, "Video", TypeMaster},
	IDFlagInterlaced:          {IDFlagInterlaced, "FlagInterlaced", TypeUint},
	IDFieldOrder:              {IDFieldOrder, "FieldOrder", TypeUint},
	IDStereoMode:              {IDStereoMode, "StereoMode", TypeUint},
	IDAlphaMode:               {IDAlphaMode, "AlphaMode", TypeUint},
	IDOldStereoMode:           {IDOldStereoMode, "OldStereoMode", TypeUint},
	IDPixelWidth:              {IDPixelWidth, "PixelWidth", TypeUint},
	IDPixelHeight:             {IDPixelHeight, "PixelHeight", TypeUint},
	IDPixelCropBottom:         {IDPixelCropBottom, "PixelCropBottom", TypeUint},
	IDPixelCropTop:            {IDPixelCropTop, "PixelCropTop", TypeUint},
	IDPixelCropLeft:           {IDPixelCropLeft, "PixelCropLeft", TypeUint},
	IDPixelCropRight:          {IDPixelCropRight, "PixelCropRight", TypeUint},
	IDDisplayWidth:            {IDDisplayWidth, "DisplayWidth", TypeUint},
	IDDisplayHeight:           {IDDisplayHeight, "DisplayHeight", TypeUint},
	IDDisplayUnit:             {IDDisplayUnit, "DisplayUnit", TypeUint},
	IDAspectRatioType:         {IDAspectRatioType, "AspectRatioType", TypeUint},
	IDUncompressedFourCC:      {IDUncompressedFourCC, "UncompressedFourCC", TypeBinary},
	IDGammaValue:              {IDGammaValue, "GammaValue", TypeFloat},
	IDFrameRate:               {IDFrameRate, "FrameRate", TypeFloat},
	IDColour:                  {IDColour, "Colour", TypeMaster},
	IDMatrixCoefficients:      {IDMatrixCoefficients, "MatrixCoefficients", TypeUint},
	IDBitsPerChannel:          {IDBitsPerChannel, "BitsPerChannel", TypeUint},
	IDChromaSubsamplingHorz:   {IDChromaSubsamplingHorz, "ChromaSubsamplingHorz", TypeUint},
	IDChromaSubsamplingVert:   {IDChromaSubsamplingVert, "ChromaSubsamplingVert", TypeUint},
	IDCbSubsamplingHorz:       {IDCbSubsamplingHorz, "CbSubsamplingHorz", TypeUint},
	IDCbSubsamplingVert:       {IDCbSubsamplingVert, "CbSubsamplingVert", TypeUint},
	IDChromaSitingHorz:        {IDChromaSitingHorz, "ChromaSitingHorz", TypeUint},
	IDChromaSitingVert:        {IDChromaSitingVert, "ChromaSitingVert", TypeUint},
	IDRange:                   {IDRange, "Range", TypeUint},
	IDTransferCharacteristics: {IDTransferCharacteristics, "TransferCharacteristics", TypeUint},
	IDPrimaries:               {IDPrimaries, "Primaries", TypeUint},
	IDMaxCLL:                  {IDMaxCLL, "MaxCLL", TypeUint},
	IDMaxFALL:                 {IDMaxFALL, "MaxFALL", TypeUint},
	IDMasteringMetadata:       {IDMasteringMetadata, "MasteringMetadata", TypeMaster},
	IDPrimaryRChromaticityX:   {IDPrimaryRChromaticityX, "PrimaryRChromaticityX", TypeFloat},
	IDPrimaryRChromaticityY:   {IDPrimaryRChromaticityY, "PrimaryRChromaticityY", TypeFloat},
	IDPrimaryGChromaticityX:   {IDPrimaryGChromaticityX, "PrimaryGChromaticityX", TypeFloat},
	IDPrimaryGChromaticityY:   {IDPrimaryGChromaticityY, "PrimaryGChromaticityY", TypeFloat},
	IDPrimaryBChromaticityX:   {IDPrimaryBChromaticityX, "PrimaryBChromaticityX", TypeFloat},
	IDPrimaryBChromaticityY:   {IDPrimaryBChromaticityY, "PrimaryBChromaticityY", TypeFloat},
	IDWhitePointChromaticityX: {IDWhitePointChromaticityX, "WhitePointChromaticityX", TypeFloat},
	IDWhitePointChromaticityY: {IDWhitePointChromaticityY, "WhitePointChromaticityY", TypeFloat},
	IDLuminanceMax:            {IDLuminanceMax, "LuminanceMax", TypeFloat},
	IDLuminanceMin:            {IDLuminanceMin, "LuminanceMin", TypeFloat},
	IDProjection:              {IDProjection, "Projection", TypeMaster},
	IDProjectionType:          {IDProjectionType, "ProjectionType", TypeUint},
	IDProjectionPrivate:       {IDProjectionPrivate, "ProjectionPrivate", TypeBinary},
	IDProjectionPoseYaw:       {IDProjectionPoseYaw, "ProjectionPoseYaw", TypeFloat},
	IDProjectionPosePitch:     {IDProjectionPosePitch, "ProjectionPosePitch", TypeFloat},
	IDProjectionPoseRoll:      {IDProjectionPoseRoll, "ProjectionPoseRoll", TypeFloat},

	// TrackEntry\Audio.
	IDAudio:                   {IDAudio, "Audio", TypeMaster},
	IDSamplingFrequency:       {IDSamplingFrequency, "SamplingFrequency", TypeFloat},
	IDOutputSamplingFrequency: {IDOutputSamplingFrequency, "OutputSamplingFrequency", TypeFloat},
	IDChannels:                {IDChannels, "Channels", TypeUint},
	IDChannelPositions:        {IDChannelPositions, "ChannelPositions", TypeBinary},
	IDBitDepth:                {IDBitDepth, "BitDepth", TypeUint},
	IDEmphasis:                {IDEmphasis, "Emphasis", TypeUint},

	// TrackEntry\ContentEncodings.
	IDContentEncodings:      {IDContentEncodings, "ContentEncodings", TypeMaster},
	IDContentEncoding:       {IDContentEncoding, "ContentEncoding", TypeMaster},
	IDContentEncodingOrder:  {IDContentEncodingOrder, "ContentEncodingOrder", TypeUint},
	IDContentEncodingScope:  {IDContentEncodingScope, "ContentEncodingScope", TypeUint},
	IDContentEncodingType:   {IDContentEncodingType, "ContentEncodingType", TypeUint},
	IDContentCompression:    {IDContentCompression, "ContentCompression", TypeMaster},
	IDContentCompAlgo:       {IDContentCompAlgo, "ContentCompAlgo", TypeUint},
	IDContentCompSettings:   {IDContentCompSettings, "ContentCompSettings", TypeBinary},
	IDContentEncryption:     {IDContentEncryption, "ContentEncryption", TypeMaster},
	IDContentEncAlgo:        {IDContentEncAlgo, "ContentEncAlgo", TypeUint},
	IDContentEncKeyID:       {IDContentEncKeyID, "ContentEncKeyID", TypeBinary},
	IDContentEncAESSettings: {IDContentEncAESSettings, "ContentEncAESSettings", TypeMaster},
	IDAESSettingsCipherMode: {IDAESSettingsCipherMode, "AESSettingsCipherMode", TypeUint},
	IDContentSignature:      {IDContentSignature, "ContentSignature", TypeBinary},
	IDContentSigKeyID:       {IDContentSigKeyID, "ContentSigKeyID", TypeBinary},
	IDContentSigAlgo:        {IDContentSigAlgo, "ContentSigAlgo", TypeUint},
	IDContentSigHashAlgo:    {IDContentSigHashAlgo, "ContentSigHashAlgo", TypeUint},

	// Cluster and BlockGroup.
	IDCluster:      {IDCluster, "Cluster", TypeMaster},
	IDTimestamp:    {IDTimestamp, "Timestamp", TypeUint},
	IDPosition:     {IDPosition, "Position", TypeUint},
	IDPrevSize:     {IDPrevSize, "PrevSize", TypeUint},
	IDBlockGroup:   {IDBlockGroup, "BlockGroup", TypeMaster},
	IDBlockVirtual: {IDBlockVirtual, "BlockVirtual", TypeBinary},
	// SimpleBlock and Block: RFC 9559 types them binary; TypeBlock is a
	// library-level refinement so callers can decode the payload with
	// parser.ParseSimpleBlock.
	IDSimpleBlock:        {IDSimpleBlock, "SimpleBlock", TypeBlock},
	IDBlock:              {IDBlock, "Block", TypeBlock},
	IDBlockAdditions:     {IDBlockAdditions, "BlockAdditions", TypeMaster},
	IDBlockMore:          {IDBlockMore, "BlockMore", TypeMaster},
	IDBlockAddID:         {IDBlockAddID, "BlockAddID", TypeUint},
	IDBlockAdditional:    {IDBlockAdditional, "BlockAdditional", TypeBinary},
	IDBlockDuration:      {IDBlockDuration, "BlockDuration", TypeUint},
	IDReferencePriority:  {IDReferencePriority, "ReferencePriority", TypeUint},
	IDReferenceBlock:     {IDReferenceBlock, "ReferenceBlock", TypeInt},
	IDReferenceVirtual:   {IDReferenceVirtual, "ReferenceVirtual", TypeInt},
	IDCodecState:         {IDCodecState, "CodecState", TypeBinary},
	IDDiscardPadding:     {IDDiscardPadding, "DiscardPadding", TypeInt},
	IDSlices:             {IDSlices, "Slices", TypeMaster},
	IDTimeSlice:          {IDTimeSlice, "TimeSlice", TypeMaster},
	IDLaceNumber:         {IDLaceNumber, "LaceNumber", TypeUint},
	IDFrameNumber:        {IDFrameNumber, "FrameNumber", TypeUint},
	IDBlockAdditionID:    {IDBlockAdditionID, "BlockAdditionID", TypeUint},
	IDDelay:              {IDDelay, "Delay", TypeUint},
	IDSliceDuration:      {IDSliceDuration, "SliceDuration", TypeUint},
	IDReferenceFrame:     {IDReferenceFrame, "ReferenceFrame", TypeMaster},
	IDReferenceOffset:    {IDReferenceOffset, "ReferenceOffset", TypeUint},
	IDReferenceTimestamp: {IDReferenceTimestamp, "ReferenceTimestamp", TypeUint},

	// Cues.
	IDCues:                {IDCues, "Cues", TypeMaster},
	IDCuePoint:            {IDCuePoint, "CuePoint", TypeMaster},
	IDCueTime:             {IDCueTime, "CueTime", TypeUint},
	IDCueTrackPositions:   {IDCueTrackPositions, "CueTrackPositions", TypeMaster},
	IDCueTrack:            {IDCueTrack, "CueTrack", TypeUint},
	IDCueClusterPosition:  {IDCueClusterPosition, "CueClusterPosition", TypeUint},
	IDCueRelativePosition: {IDCueRelativePosition, "CueRelativePosition", TypeUint},
	IDCueDuration:         {IDCueDuration, "CueDuration", TypeUint},
	IDCueBlockNumber:      {IDCueBlockNumber, "CueBlockNumber", TypeUint},
	IDCueCodecState:       {IDCueCodecState, "CueCodecState", TypeUint},
	IDCueReference:        {IDCueReference, "CueReference", TypeMaster},
	IDCueRefTime:          {IDCueRefTime, "CueRefTime", TypeUint},
	IDCueRefCluster:       {IDCueRefCluster, "CueRefCluster", TypeUint},
	IDCueRefNumber:        {IDCueRefNumber, "CueRefNumber", TypeUint},
	IDCueRefCodecState:    {IDCueRefCodecState, "CueRefCodecState", TypeUint},

	// Tags.
	IDTags:               {IDTags, "Tags", TypeMaster},
	IDTag:                {IDTag, "Tag", TypeMaster},
	IDTargets:            {IDTargets, "Targets", TypeMaster},
	IDTargetTypeValue:    {IDTargetTypeValue, "TargetTypeValue", TypeUint},
	IDTargetType:         {IDTargetType, "TargetType", TypeString},
	IDTagTrackUID:        {IDTagTrackUID, "TagTrackUID", TypeUint},
	IDTagEditionUID:      {IDTagEditionUID, "TagEditionUID", TypeUint},
	IDTagChapterUID:      {IDTagChapterUID, "TagChapterUID", TypeUint},
	IDTagAttachmentUID:   {IDTagAttachmentUID, "TagAttachmentUID", TypeUint},
	IDTagBlockAddIDValue: {IDTagBlockAddIDValue, "TagBlockAddIDValue", TypeUint},
	IDSimpleTag:          {IDSimpleTag, "SimpleTag", TypeMaster},
	IDTagName:            {IDTagName, "TagName", TypeUTF8},
	IDTagLanguage:        {IDTagLanguage, "TagLanguage", TypeString},
	IDTagLanguageBCP47:   {IDTagLanguageBCP47, "TagLanguageBCP47", TypeString},
	IDTagDefault:         {IDTagDefault, "TagDefault", TypeUint},
	IDTagDefaultBogus:    {IDTagDefaultBogus, "TagDefaultBogus", TypeUint},
	IDTagString:          {IDTagString, "TagString", TypeUTF8},
	IDTagBinary:          {IDTagBinary, "TagBinary", TypeBinary},

	// Chapters.
	IDChapters:                 {IDChapters, "Chapters", TypeMaster},
	IDEditionEntry:             {IDEditionEntry, "EditionEntry", TypeMaster},
	IDEditionUID:               {IDEditionUID, "EditionUID", TypeUint},
	IDEditionFlagHidden:        {IDEditionFlagHidden, "EditionFlagHidden", TypeUint},
	IDEditionFlagDefault:       {IDEditionFlagDefault, "EditionFlagDefault", TypeUint},
	IDEditionFlagOrdered:       {IDEditionFlagOrdered, "EditionFlagOrdered", TypeUint},
	IDEditionDisplay:           {IDEditionDisplay, "EditionDisplay", TypeMaster},
	IDEditionString:            {IDEditionString, "EditionString", TypeUTF8},
	IDEditionLanguageIETF:      {IDEditionLanguageIETF, "EditionLanguageIETF", TypeString},
	IDChapterAtom:              {IDChapterAtom, "ChapterAtom", TypeMaster},
	IDChapterUID:               {IDChapterUID, "ChapterUID", TypeUint},
	IDChapterStringUID:         {IDChapterStringUID, "ChapterStringUID", TypeUTF8},
	IDChapterTimeStart:         {IDChapterTimeStart, "ChapterTimeStart", TypeUint},
	IDChapterTimeEnd:           {IDChapterTimeEnd, "ChapterTimeEnd", TypeUint},
	IDChapterFlagHidden:        {IDChapterFlagHidden, "ChapterFlagHidden", TypeUint},
	IDChapterFlagEnabled:       {IDChapterFlagEnabled, "ChapterFlagEnabled", TypeUint},
	IDChapterSegmentUUID:       {IDChapterSegmentUUID, "ChapterSegmentUUID", TypeBinary},
	IDChapterSkipType:          {IDChapterSkipType, "ChapterSkipType", TypeUint},
	IDChapterSegmentEditionUID: {IDChapterSegmentEditionUID, "ChapterSegmentEditionUID", TypeUint},
	IDChapterPhysicalEquiv:     {IDChapterPhysicalEquiv, "ChapterPhysicalEquiv", TypeUint},
	IDChapterTrack:             {IDChapterTrack, "ChapterTrack", TypeMaster},
	IDChapterTrackUID:          {IDChapterTrackUID, "ChapterTrackUID", TypeUint},
	IDChapterDisplay:           {IDChapterDisplay, "ChapterDisplay", TypeMaster},
	IDChapString:               {IDChapString, "ChapString", TypeUTF8},
	IDChapLanguage:             {IDChapLanguage, "ChapLanguage", TypeString},
	IDChapLanguageBCP47:        {IDChapLanguageBCP47, "ChapLanguageBCP47", TypeString},
	IDChapCountry:              {IDChapCountry, "ChapCountry", TypeString},
	IDChapProcess:              {IDChapProcess, "ChapProcess", TypeMaster},
	IDChapProcessCodecID:       {IDChapProcessCodecID, "ChapProcessCodecID", TypeUint},
	IDChapProcessPrivate:       {IDChapProcessPrivate, "ChapProcessPrivate", TypeBinary},
	IDChapProcessCommand:       {IDChapProcessCommand, "ChapProcessCommand", TypeMaster},
	IDChapProcessTime:          {IDChapProcessTime, "ChapProcessTime", TypeUint},
	IDChapProcessData:          {IDChapProcessData, "ChapProcessData", TypeBinary},

	// Attachments.
	IDAttachments:       {IDAttachments, "Attachments", TypeMaster},
	IDAttachedFile:      {IDAttachedFile, "AttachedFile", TypeMaster},
	IDFileDescription:   {IDFileDescription, "FileDescription", TypeUTF8},
	IDFileName:          {IDFileName, "FileName", TypeUTF8},
	IDFileMediaType:     {IDFileMediaType, "FileMediaType", TypeString},
	IDFileData:          {IDFileData, "FileData", TypeBinary},
	IDFileUID:           {IDFileUID, "FileUID", TypeUint},
	IDFileReferral:      {IDFileReferral, "FileReferral", TypeBinary},
	IDFileUsedStartTime: {IDFileUsedStartTime, "FileUsedStartTime", TypeUint},
	IDFileUsedEndTime:   {IDFileUsedEndTime, "FileUsedEndTime", TypeUint},
}

// Global elements the schema allows inside any master.
var globalElements = map[parser.ElementID]struct{}{
	IDCRC32: {},
	IDVoid:  {},
}

// Complete child lists for the only masters whose direct containment is
// enumerated here. Deprecated Cluster children (SilentTracks and
// EncryptedBlock) are deliberately absent: an unknown or unregistered child
// must never cause an early boundary.
//
// That omission is safe ONLY while those two stay out of the elements table
// above, because EndsUnknownSizeMaster refuses to end a master on an
// unregistered ID. Registering either without listing it here would make it end
// every unknown-size Cluster it appears in, which is why the two are the only
// elements in the schema this registry deliberately does not name;
// internal/specconform reports the pairing as a MISMATCH if it is ever broken.
var completeChildren = map[parser.ElementID][]parser.ElementID{
	IDSegment: {
		IDSeekHead, IDInfo, IDCluster, IDTracks, IDCues, IDAttachments, IDChapters, IDTags,
	},
	IDCluster: {
		IDTimestamp, IDPosition, IDPrevSize, IDSimpleBlock, IDBlockGroup,
	},
}

// names is the reverse index of elements, keyed by the canonical RFC 9559 name.
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
// Aliases are added only for renames this repository can point at a source for.
// The schema's `<extension type="libmatroska" cppname="..."/>` values look like
// old element names (PrevUID, TrackTimecodeScale, ChapterSegmentUID) but are
// libmatroska's C++ class names, so they are NOT evidence of a pre-RFC element
// name and none of them is aliased here on that basis.
var legacyNames = map[string]parser.ElementID{
	"SegmentUID":    IDSegmentUUID,   // RFC 9559 renamed SegmentUID to SegmentUUID.
	"FileMimeType":  IDFileMediaType, // RFC 9559 renamed FileMimeType to FileMediaType.
	"Timecode":      IDTimestamp,     // RFC 9559 Section 11 renamed Timecode to Timestamp.
	"TimecodeScale": IDTimestampScale,
}
