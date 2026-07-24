// Common constants used together with the streaming EBML parser.
// Keep these in sync across languages.

export const UNKNOWN_SIZE = -1 as const;

export const MAX_ELEMENT_ID_LENGTH = 4 as const;
export const MAX_ELEMENT_SIZE_LENGTH = 8 as const;

// WebM/Matroska element IDs (partial; extend as needed).
export const ELEMENT_ID = {
  EBML: 0x1a45dfa3,
  Segment: 0x18538067,
  EBMLVersion: 0x4286,
  EBMLReadVersion: 0x42f7,
  DocType: 0x4282,
  Void: 0xec,
} as const;

export type ElementID = (typeof ELEMENT_ID)[keyof typeof ELEMENT_ID] | number;

export function nameForElementID(id: ElementID): string | undefined {
  switch (id) {
    case ELEMENT_ID.EBML:
      return "EBML";
    case ELEMENT_ID.Segment:
      return "Segment";
    case ELEMENT_ID.EBMLVersion:
      return "EBMLVersion";
    case ELEMENT_ID.EBMLReadVersion:
      return "EBMLReadVersion";
    case ELEMENT_ID.DocType:
      return "DocType";
    case ELEMENT_ID.Void:
      return "Void";
    default:
      return undefined;
  }
}

