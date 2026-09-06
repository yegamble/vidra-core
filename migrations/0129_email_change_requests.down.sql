-- Down: drop the email-change request table. Pending (unconfirmed) requests are
-- lost, which is the safe direction — a dropped pending request cannot move an
-- address; already-confirmed changes live in users.email and are unaffected.
DROP TABLE IF EXISTS email_change_requests;
