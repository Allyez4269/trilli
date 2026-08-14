-- Restore the sequence-backed TKT- default (existing opaque refs are kept).
CREATE SEQUENCE IF NOT EXISTS support_ticket_number_seq START 1000;
ALTER TABLE support_tickets
    ALTER COLUMN ticket_number
    SET DEFAULT ('TKT-' || lpad(nextval('support_ticket_number_seq')::text, 6, '0'));
