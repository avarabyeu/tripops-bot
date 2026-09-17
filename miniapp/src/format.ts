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
