/**
 * The API client.
 *
 * One rule: every request carries the signed Telegram launch parameters, and
 * the backend re-verifies them. There is no login, no token exchange and no
 * session to keep alive.
 */

import { initData } from "./telegram";
import type {
  Accommodation,
  ActivityEntry,
  BalanceReport,
  Checklist,
  Dashboard,
  Decision,
  Expense,
  ExpenseReport,
  Invite,
  InvitePreview,
  Me,
  Member,
  NotificationPreferences,
  Progress,
  Settlement,
  Trip,
  TripEvent,
  TripSummary,
  Vehicle,
} from "./types";

const BASE = "/api/v1";

/** An error the UI can show as-is. */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly fields: Record<string, string>;

  constructor(status: number, code: string, message: string, fields: Record<string, string> = {}) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields;
  }

  /** Retrying makes sense for a server hiccup, not for a rejected request. */
  get retryable(): boolean {
    return this.status >= 500 || this.status === 0;
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let response: Response;
  try {
    response = await fetch(BASE + path, {
      method,
      headers: {
        Authorization: `tma ${initData()}`,
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "network", "No connection. Check your network and try again.");
  }

  if (response.status === 204) return undefined as T;

  const text = await response.text();
  const payload = text ? safeParse(text) : undefined;

  if (!response.ok) {
    const error = (payload as { error?: { code?: string; message?: string; fields?: Record<string, string> } })
      ?.error;
    throw new ApiError(
      response.status,
      error?.code ?? "unknown",
      error?.message ?? `Request failed (${response.status})`,
      error?.fields ?? {},
    );
  }
  return payload as T;
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

const get = <T>(path: string) => request<T>("GET", path);
const post = <T>(path: string, body?: unknown) => request<T>("POST", path, body ?? {});
const patch = <T>(path: string, body: unknown) => request<T>("PATCH", path, body);
const del = (path: string) => request<void>("DELETE", path);

const trip = (tripId: string) => `${"/trips/"}${tripId}`;

export const api = {
  me: () => get<Me>("/me"),

  preferences: {
    get: () => get<NotificationPreferences>("/me/notification-preferences"),
    save: (prefs: Partial<NotificationPreferences>) =>
      patch<NotificationPreferences>("/me/notification-preferences", prefs),
  },

  trips: {
    list: () => get<{ trips: TripSummary[] }>("/trips").then((r) => r.trips),
    create: (input: {
      title: string;
      description?: string;
      start_date: string;
      end_date: string;
      timezone: string;
      currency: string;
    }) => post<Trip>("/trips", input),
    get: (id: string) => get<{ trip: Trip; me: Member }>(trip(id)),
    update: (id: string, input: Record<string, unknown>) => patch<Trip>(trip(id), input),
    remove: (id: string) => del(trip(id)),
    dashboard: (id: string) => get<Dashboard>(`${trip(id)}/dashboard`),
    activity: (id: string) =>
      get<{ activity: ActivityEntry[] }>(`${trip(id)}/activity`).then((r) => r.activity),
  },

  members: {
    list: (tripId: string) =>
      get<{ members: Member[] }>(`${trip(tripId)}/members`).then((r) => r.members),
    update: (tripId: string, memberId: string, input: Record<string, unknown>) =>
      patch<Member>(`${trip(tripId)}/members/${memberId}`, input),
    remove: (tripId: string, memberId: string) => del(`${trip(tripId)}/members/${memberId}`),
  },

  invites: {
    list: (tripId: string) =>
      get<{ invites: Invite[] }>(`${trip(tripId)}/invites`).then((r) => r.invites),
    create: (tripId: string) => post<Invite>(`${trip(tripId)}/invites`, { role: "member" }),
    revoke: (tripId: string, inviteId: string) => del(`${trip(tripId)}/invites/${inviteId}`),
    preview: (token: string) => get<InvitePreview>(`/invites/${token}`),
    join: (token: string) => post<{ trip: Trip; member: Member }>(`/invites/${token}/join`),
  },

  events: {
    list: (tripId: string) =>
      get<{ events: TripEvent[] }>(`${trip(tripId)}/events`).then((r) => r.events),
    create: (tripId: string, input: Record<string, unknown>) =>
      post<TripEvent>(`${trip(tripId)}/events`, input),
    update: (tripId: string, id: string, input: Record<string, unknown>) =>
      patch<TripEvent>(`${trip(tripId)}/events/${id}`, input),
    remove: (tripId: string, id: string) => del(`${trip(tripId)}/events/${id}`),
    rsvp: (tripId: string, id: string, status: string, memberId?: string) =>
      post<TripEvent>(`${trip(tripId)}/events/${id}/rsvp`, {
        status,
        ...(memberId ? { member_id: memberId } : {}),
      }),
  },

  decisions: {
    list: (tripId: string) =>
      get<{ decisions: Decision[] }>(`${trip(tripId)}/decisions`).then((r) => r.decisions),
    create: (tripId: string, input: { title: string; options: string[]; deadline?: string }) =>
      post<Decision>(`${trip(tripId)}/decisions`, input),
    vote: (tripId: string, id: string, optionId: string) =>
      post<Decision>(`${trip(tripId)}/decisions/${id}/vote`, { option_id: optionId }),
    close: (tripId: string, id: string) => post<Decision>(`${trip(tripId)}/decisions/${id}/close`),
    resolve: (tripId: string, id: string, optionId: string, note = "") =>
      post<Decision>(`${trip(tripId)}/decisions/${id}/resolve`, { option_id: optionId, note }),
  },

  vehicles: {
    list: (tripId: string) =>
      get<{ vehicles: Vehicle[] }>(`${trip(tripId)}/vehicles`).then((r) => r.vehicles),
    create: (tripId: string, input: Record<string, unknown>) =>
      post<Vehicle>(`${trip(tripId)}/vehicles`, input),
    update: (tripId: string, id: string, input: Record<string, unknown>) =>
      patch<Vehicle>(`${trip(tripId)}/vehicles/${id}`, input),
    remove: (tripId: string, id: string) => del(`${trip(tripId)}/vehicles/${id}`),
    join: (tripId: string, id: string) => post<Vehicle>(`${trip(tripId)}/vehicles/${id}/join`),
    leave: (tripId: string, id: string) => post<Vehicle>(`${trip(tripId)}/vehicles/${id}/leave`),
  },

  accommodation: {
    list: (tripId: string) =>
      get<{ accommodations: Accommodation[] }>(`${trip(tripId)}/accommodations`).then(
        (r) => r.accommodations,
      ),
    create: (tripId: string, input: Record<string, unknown>) =>
      post<Accommodation>(`${trip(tripId)}/accommodations`, input),
    remove: (tripId: string, id: string) => del(`${trip(tripId)}/accommodations/${id}`),
    setGuestStatus: (tripId: string, id: string, status: string, memberId?: string) =>
      post<Accommodation>(`${trip(tripId)}/accommodations/${id}/guest-status`, {
        status,
        ...(memberId ? { member_id: memberId } : {}),
      }),
  },

  checklists: {
    list: (tripId: string) =>
      get<{ checklists: Checklist[]; progress: Progress }>(`${trip(tripId)}/checklists`),
    create: (tripId: string, input: { title: string; scope: string; items?: string[] }) =>
      post<Checklist>(`${trip(tripId)}/checklists`, input),
    remove: (tripId: string, id: string) => del(`${trip(tripId)}/checklists/${id}`),
    addItem: (tripId: string, listId: string, title: string) =>
      post(`${trip(tripId)}/checklists/${listId}/items`, { title }),
    toggleItem: (tripId: string, itemId: string, completed: boolean) =>
      patch(`${trip(tripId)}/checklist-items/${itemId}`, { completed }),
    removeItem: (tripId: string, itemId: string) => del(`${trip(tripId)}/checklist-items/${itemId}`),
  },

  expenses: {
    list: (tripId: string) =>
      get<{ expenses: Expense[] }>(`${trip(tripId)}/expenses`).then((r) => r.expenses),
    create: (tripId: string, input: Record<string, unknown>) =>
      post<Expense>(`${trip(tripId)}/expenses`, input),
    update: (tripId: string, id: string, input: Record<string, unknown>) =>
      patch<Expense>(`${trip(tripId)}/expenses/${id}`, input),
    remove: (tripId: string, id: string) => del(`${trip(tripId)}/expenses/${id}`),
    report: (tripId: string) => get<ExpenseReport>(`${trip(tripId)}/expenses/report`),
    balances: (tripId: string) => get<BalanceReport>(`${trip(tripId)}/balances`),
    settle: (tripId: string, input: { from_member_id: string; to_member_id: string; amount_minor: number }) =>
      post<Settlement>(`${trip(tripId)}/settlements`, input),
  },
};
