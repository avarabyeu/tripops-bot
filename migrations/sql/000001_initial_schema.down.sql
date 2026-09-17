-- Reverse of 000001_initial_schema. Dropped in reverse dependency order so
-- foreign keys stay satisfied on engines that check them during DDL.

DROP TABLE IF EXISTS activity_log;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS notification_preferences;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS settlements;
DROP TABLE IF EXISTS expense_participants;
DROP TABLE IF EXISTS expenses;
DROP TABLE IF EXISTS checklist_items;
DROP TABLE IF EXISTS checklists;
DROP TABLE IF EXISTS accommodation_guests;
DROP TABLE IF EXISTS accommodations;
DROP TABLE IF EXISTS vehicle_passengers;
DROP TABLE IF EXISTS vehicles;
DROP TABLE IF EXISTS decision_votes;
DROP TABLE IF EXISTS decision_options;
DROP TABLE IF EXISTS decisions;
DROP TABLE IF EXISTS event_participants;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS trip_invites;
DROP TABLE IF EXISTS trip_members;
DROP TABLE IF EXISTS trips;
DROP TABLE IF EXISTS users;
