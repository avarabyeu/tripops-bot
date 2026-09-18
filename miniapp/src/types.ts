/** Mirrors of the backend's JSON shapes. Only the fields the UI reads. */

export type Role = "owner" | "admin" | "member";
export type TripStatus = "planning" | "active" | "completed" | "archived";

export interface Trip {
  id: string;
  title: string;
  description: string;
  start_date: string;
  end_date: string;
  timezone: string;
  currency: string;
  owner_id: string;
  status: TripStatus;
}

export interface TripSummary extends Trip {
  role: Role;
  member_count: number;
  open_decisions: number;
}

export interface Member {
  id: string;
  user_id: string;
  display_name: string;
  role: Role;
  status: "active" | "declined" | "removed";
  username?: string;
  photo_url?: string;
}

export type RSVP = "attending" | "not_attending" | "maybe" | "undecided";

export interface EventParticipant {
  member_id: string;
  display_name: string;
  status: RSVP;
}

export type EventType =
  | "departure"
  | "arrival"
  | "accommodation"
  | "activity"
  | "meal"
  | "transport"
  | "race"
  | "custom";

export interface TripEvent {
  id: string;
  title: string;
  description: string;
  start_at: string;
  end_at?: string;
  location_name?: string;
  type: EventType;
  participants: EventParticipant[];
  attending: number;
  undecided: number;
}

export interface DecisionOption {
  id: string;
  label: string;
  votes: number;
  voters: { member_id: string; display_name: string }[];
}

export interface Decision {
  id: string;
  title: string;
  description?: string;
  deadline?: string;
  status: "open" | "closed" | "resolved" | "cancelled";
  options: DecisionOption[];
  my_vote?: string;
  resolved_option_id?: string;
  resolution_note?: string;
  eligible: number;
  total_votes: number;
  pending: { member_id: string; display_name: string }[];
}

export interface Vehicle {
  id: string;
  name: string;
  type: "car" | "van" | "train" | "bus" | "other";
  capacity: number;
  notes?: string;
  driver_member_id?: string;
  driver_name?: string;
  passengers: { member_id: string; display_name: string }[];
  seats_used: number;
  seats_left: number;
}

export type GuestStatus = "pending" | "confirmed" | "declined";

export interface Accommodation {
  id: string;
  name: string;
  address?: string;
  url?: string;
  check_in?: string;
  check_out?: string;
  capacity: number;
  notes?: string;
  guests: { member_id: string; display_name: string; status: GuestStatus }[];
  confirmed: number;
  pending: number;
  occupied: number;
}

export interface ChecklistItem {
  id: string;
  title: string;
  completed: boolean;
  assigned_to?: string;
  assigned_to_name?: string;
  due_at?: string;
}

export interface Checklist {
  id: string;
  title: string;
  scope: "shared" | "personal";
  owner_member_id?: string;
  items: ChecklistItem[];
  progress: Progress;
}

export interface Progress {
  completed: number;
  total: number;
}

export type ExpenseCategory =
  | "accommodation"
  | "transport"
  | "fuel"
  | "food"
  | "parking"
  | "registration"
  | "equipment"
  | "other";

export interface Expense {
  id: string;
  title: string;
  amount_minor: number;
  currency: string;
  category: ExpenseCategory;
  paid_by: string;
  paid_by_name?: string;
  /** The user who recorded it — not a member id. They may edit it; so may the owner. */
  created_by: string;
  split_type: "equal" | "custom_amount" | "percentage";
  spent_at: string;
  participants: { member_id: string; display_name?: string; share_minor: number }[];
}

/** One person's line in the expense report. */
export interface MemberSummary {
  member_id: string;
  display_name: string;
  paid_minor: number;
  paid_count: number;
  share_minor: number;
  share_count: number;
  settled_minor: number;
  balance_minor: number;
}

export interface CategoryTotal {
  category: ExpenseCategory;
  total_minor: number;
  count: number;
  percent: number;
}

/** The whole ledger: what the trip cost, where it went, where everyone stands. */
export interface ExpenseReport {
  currency: string;
  total_minor: number;
  count: number;
  per_person_minor: number;
  by_category: CategoryTotal[];
  members: MemberSummary[];
  expenses: Expense[];
}

export interface Balance {
  member_id: string;
  display_name: string;
  paid_minor: number;
  owed_minor: number;
  balance_minor: number;
}

export interface Transfer {
  from_member_id: string;
  from_name: string;
  to_member_id: string;
  to_name: string;
  amount_minor: number;
  currency: string;
}

export interface BalanceReport {
  currency: string;
  total_minor: number;
  balances: Balance[];
  transfers: Transfer[];
  settlements: Settlement[];
}

export interface Settlement {
  id: string;
  from_member_id: string;
  from_name: string;
  to_member_id: string;
  to_name: string;
  amount_minor: number;
  currency: string;
  status: "pending" | "settled" | "cancelled";
}

export interface AttentionItem {
  type: string;
  severity: "info" | "warning" | "critical";
  title: string;
  description: string;
  target: { kind: string; id?: string };
  mine: boolean;
}

export interface Dashboard {
  trip: Trip;
  me: Member;
  people: { active: number };
  transport: { vehicles: number; seats: number; seats_used: number; unseated: number };
  accommodation: { places: number; confirmed: number; total: number };
  next_event?: TripEvent;
  expenses: { total_minor: number; currency: string; count: number; my_balance_minor: number };
  decisions: { open: number; awaiting_my_vote: number };
  checklist: Progress;
  attention: AttentionItem[];
  days_until_start: number;
}

export interface ActivityEntry {
  id: string;
  actor_name: string;
  kind: string;
  message: string;
  created_at: string;
}

export interface Invite {
  id: string;
  token: string;
  url: string;
  role: Role;
  uses: number;
  max_uses: number;
}

export interface InvitePreview {
  trip: Trip;
  owner_name: string;
  member_count: number;
  already_joined: boolean;
  role: Role;
}

export interface Me {
  id: string;
  telegram_id: number;
  name: string;
  username?: string;
  photo_url?: string;
  start_param?: string;
}

export interface NotificationPreferences {
  trip_updates: boolean;
  decisions: boolean;
  reminders: boolean;
  checklist: boolean;
  expenses: boolean;
}
