/**
 * Formatting helpers.
 *
 * Money arrives from the API as integer minor units and is only ever turned
 * into a string here; no arithmetic in this file rounds anything.
 */

const SYMBOLS: Record<string, string> = {
  EUR: "€",
  USD: "$",
  GBP: "£",
  PLN: "zł",
  UAH: "₴",
  CZK: "Kč",
};

export function money(minor: number, currency: string): string {
  const symbol = SYMBOLS[currency];
  const value = (Math.abs(minor) / 100).toFixed(2);
  const sign = minor < 0 ? "-" : "";
  return symbol ? `${sign}${symbol}${value}` : `${sign}${value} ${currency}`;
}

/** Renders a balance with an explicit sign, which is the point of a balance. */
export function signedMoney(minor: number, currency: string): string {
  return minor > 0 ? `+${money(minor, currency)}` : money(minor, currency);
}

/** Parses "12", "12.50" or "12,50" into minor units; NaN when unparseable. */
export function parseMoney(input: string): number {
  const cleaned = input.trim().replace(/\s/g, "").replace(",", ".");
  if (!/^\d+(\.\d{1,2})?$/.test(cleaned)) return Number.NaN;
  return Math.round(Number(cleaned) * 100);
}

/** Formats an instant in the trip's timezone, never the phone's. */
export function timeIn(iso: string, timezone: string): string {
  return new Date(iso).toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    timeZone: timezone,
    hour12: false,
  });
}

export function dayIn(iso: string, timezone: string): string {
  return new Date(iso).toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    weekday: "short",
    timeZone: timezone,
  });
}

/**
 * The wall-clock parts of an instant in a given timezone.
 *
 * `en-CA` because it formats as YYYY-MM-DD, which is the one locale output
 * worth parsing; everything else here goes through `Intl` for display only.
 */
function partsIn(date: Date, timezone: string) {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: timezone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).formatToParts(date);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value ?? "0");
  // hourCycle h23 can render midnight as 24; Date.UTC normalises it anyway.
  return { y: get("year"), m: get("month"), d: get("day"), hh: get("hour"), mm: get("minute"), ss: get("second") };
}

/** How far ahead of UTC the zone is at that instant, in milliseconds. */
function offsetAt(date: Date, timezone: string): number {
  const p = partsIn(date, timezone);
  return Date.UTC(p.y, p.m - 1, p.d, p.hh, p.mm, p.ss) - date.getTime();
}

/**
 * An instant, as the `datetime-local` value a person in the trip's timezone
 * would read off a clock.
 *
 * `<input type="datetime-local">` has no timezone: it is a wall-clock string,
 * and the browser's own zone is irrelevant to it. Using `new Date(value)` on
 * the way back in — which is what this screen used to do — silently reads it
 * as the *device's* local time, so an organiser in Warsaw editing a trip kept
 * in UTC moved every event by an hour just by opening the form.
 */
export function toLocalInput(iso: string, timezone: string): string {
  const p = partsIn(new Date(iso), timezone);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${p.y}-${pad(p.m)}-${pad(p.d)}T${pad(p.hh)}:${pad(p.mm)}`;
}

/**
 * The inverse: a wall clock in the trip's timezone, as an ISO instant.
 *
 * Twice a year a wall clock is not one instant. On the autumn change 02:30
 * happens twice, and this returns the **first** — the same choice `Temporal`
 * makes, and the one that matches "the earlier of the two 02:30s". On the
 * spring change 02:30 never happens at all, and this returns the instant just
 * after the gap rather than refusing an event somebody is trying to schedule.
 */
export function fromLocalInput(value: string, timezone: string): string {
  const wall = Date.parse(`${value}:00Z`);
  const day = 86_400_000;
  // The offsets a day either side bracket any transition, so one of these two
  // candidates is right whenever the wall clock exists at all.
  const before = wall - offsetAt(new Date(wall - day), timezone);
  const after = wall - offsetAt(new Date(wall + day), timezone);

  // Earliest first, so a wall clock that happens twice resolves to the first.
  for (const candidate of [Math.min(before, after), Math.max(before, after)]) {
    const iso = new Date(candidate).toISOString();
    if (Date.parse(`${toLocalInput(iso, timezone)}:00Z`) === wall) return iso;
  }
  // The clock skipped this time. Land after the gap.
  return new Date(Math.max(before, after)).toISOString();
}

/**
 * A `datetime-local` value some days ahead at a given hour, read in the trip's
 * timezone — the default a form offers before anybody types.
 */
export function dayAheadIn(timezone: string, days: number, hour: number): string {
  const today = toLocalInput(new Date().toISOString(), timezone).slice(0, 10);
  const target = new Date(`${today}T00:00:00Z`);
  target.setUTCDate(target.getUTCDate() + days);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${target.toISOString().slice(0, 10)}T${pad(hour)}:00`;
}

export function dateTimeIn(iso: string, timezone: string): string {
  return `${dayIn(iso, timezone)} · ${timeIn(iso, timezone)}`;
}

/** "23–24 September 2026", collapsing a single day. */
export function dateRange(start: string, end: string): string {
  const from = new Date(`${start}T00:00:00Z`);
  const to = new Date(`${end}T00:00:00Z`);
  const opts: Intl.DateTimeFormatOptions = { timeZone: "UTC" };
  if (start === end) {
    return from.toLocaleDateString(undefined, { ...opts, day: "numeric", month: "long", year: "numeric" });
  }
  if (from.getUTCMonth() === to.getUTCMonth() && from.getUTCFullYear() === to.getUTCFullYear()) {
    return `${from.getUTCDate()}–${to.toLocaleDateString(undefined, {
      ...opts,
      day: "numeric",
      month: "long",
      year: "numeric",
    })}`;
  }
  return `${from.toLocaleDateString(undefined, { ...opts, day: "numeric", month: "short" })} – ${to.toLocaleDateString(
    undefined,
    { ...opts, day: "numeric", month: "short", year: "numeric" },
  )}`;
}

/** "in 2 h", "in 3 days", "now". */
export function relative(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "now";
  const minutes = Math.round(ms / 60000);
  if (minutes < 60) return `in ${minutes} min`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `in ${hours} h`;
  return `in ${Math.round(hours / 24)} days`;
}

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "??";
  if (parts.length === 1) return (parts[0] ?? "").slice(0, 2).toUpperCase();
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase();
}

/**
 * Display names for the kinds a thing can have.
 *
 * They double as the suggested name when a kind is picked, so the chip and the
 * filled-in field always read the same. A catch-all kind suggests nothing:
 * "Other" is not a name for anything.
 */
export const EVENT_TYPE_NAMES: Record<string, string> = {
  departure: "Departure",
  arrival: "Arrival",
  accommodation: "Check-in",
  activity: "Activity",
  meal: "Meal",
  transport: "Transport",
  race: "Race",
  custom: "",
};

export const CATEGORY_NAMES: Record<string, string> = {
  accommodation: "Accommodation",
  transport: "Transport",
  fuel: "Fuel",
  food: "Food",
  parking: "Parking",
  registration: "Registration",
  equipment: "Equipment",
  other: "",
};

/**
 * The currencies the backend accepts, in the order `core.SupportedCurrencies`
 * returns them. Adding one here without adding it there gets a 422.
 */
export const CURRENCIES = ["CZK", "EUR", "GBP", "PLN", "UAH", "USD"] as const;

/**
 * Every IANA zone the browser knows, falling back to the device's own when the
 * runtime is too old to enumerate them. Hard-coding a list of "likely" zones
 * is wrong for exactly the person who needs to change it.
 */
export function timezones(): string[] {
  const device = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const all = (Intl as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf?.(
    "timeZone",
  );
  if (!all || all.length === 0) return [device];
  return all.includes(device) ? all : [device, ...all];
}

export const VEHICLE_TYPE_NAMES: Record<string, string> = {
  car: "Car",
  van: "Van",
  train: "Train",
  bus: "Bus",
  other: "",
};

export const EVENT_ICONS: Record<string, string> = {
  departure: "🚗",
  arrival: "🏁",
  accommodation: "🏠",
  activity: "🎯",
  meal: "🍝",
  transport: "🚌",
  race: "🚴",
  custom: "📌",
};

export const CATEGORY_ICONS: Record<string, string> = {
  accommodation: "🏠",
  transport: "🚗",
  fuel: "⛽",
  food: "🍽",
  parking: "🅿️",
  registration: "🎫",
  equipment: "🧰",
  other: "💶",
};

export const VEHICLE_ICONS: Record<string, string> = {
  car: "🚗",
  van: "🚐",
  train: "🚆",
  bus: "🚌",
  other: "🧭",
};
