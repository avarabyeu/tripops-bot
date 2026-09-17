import { useState } from "react";

/**
 * A name field that follows the selected kind until the user makes it theirs.
 *
 * Picking "🚗 Departure" and then having to type the word "Departure" is the
 * kind of friction this product exists to remove, so choosing a kind fills the
 * name in. It stops doing that the moment the field is edited, because a
 * suggestion that overwrites what somebody typed is worse than no suggestion.
 * Clearing the field hands control back.
 */
export interface SuggestedName {
  value: string;
  /** Bind to the input's onChange. */
  edit(next: string): void;
  /** Call when the kind changes. An empty suggestion leaves the field blank. */
  suggest(next: string): void;
}

export function useSuggestedName(initial: string): SuggestedName {
  const [value, setValue] = useState(initial);
  // The initial value is a suggestion too, so it is replaceable until touched.
  const [edited, setEdited] = useState(false);

  return {
    value,
    edit(next) {
      setValue(next);
      setEdited(next.trim() !== "");
    },
    suggest(next) {
      if (!edited) setValue(next);
    },
  };
}
