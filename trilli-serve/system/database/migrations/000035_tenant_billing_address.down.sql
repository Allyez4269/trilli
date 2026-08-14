ALTER TABLE tenants DROP COLUMN IF EXISTS billing_name;
ALTER TABLE tenants DROP COLUMN IF EXISTS billing_line1;
ALTER TABLE tenants DROP COLUMN IF EXISTS billing_line2;
ALTER TABLE tenants DROP COLUMN IF EXISTS billing_city;
ALTER TABLE tenants DROP COLUMN IF EXISTS billing_state;
ALTER TABLE tenants DROP COLUMN IF EXISTS billing_postal_code;
ALTER TABLE tenants DROP COLUMN IF EXISTS billing_country;
