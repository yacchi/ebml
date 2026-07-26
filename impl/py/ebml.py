"""
Common constants used together with the streaming EBML parser.

Keep these in sync across languages.
"""

UNKNOWN_SIZE = -1

MAX_ELEMENT_ID_LENGTH = 4
MAX_ELEMENT_SIZE_LENGTH = 8

# WebM/Matroska element IDs (partial; extend as needed).
ELEMENT_ID_EBML = 0x1A45DFA3
ELEMENT_ID_SEGMENT = 0x18538067
ELEMENT_ID_EBML_VERSION = 0x4286
ELEMENT_ID_EBML_READ_VERSION = 0x42F7
ELEMENT_ID_DOC_TYPE = 0x4282
ELEMENT_ID_VOID = 0xEC


def name_for_element_id(element_id: int) -> str | None:
    return {
        ELEMENT_ID_EBML: "EBML",
        ELEMENT_ID_SEGMENT: "Segment",
        ELEMENT_ID_EBML_VERSION: "EBMLVersion",
        ELEMENT_ID_EBML_READ_VERSION: "EBMLReadVersion",
        ELEMENT_ID_DOC_TYPE: "DocType",
        ELEMENT_ID_VOID: "Void",
    }.get(element_id)

