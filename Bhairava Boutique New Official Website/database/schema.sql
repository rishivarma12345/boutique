CREATE TABLE IF NOT EXISTS products (
 id UUID PRIMARY KEY,
 name TEXT NOT NULL,
 category TEXT NOT NULL CHECK (category IN ('Bridal','Non-Bridal','Kids','Sarees','Blouses')),
 description TEXT DEFAULT '',
 price NUMERIC(12,2) NOT NULL CHECK (price >= 0),
 stock INTEGER NOT NULL DEFAULT 0 CHECK (stock >= 0),
 image_url TEXT DEFAULT '',
 active BOOLEAN NOT NULL DEFAULT TRUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS appointments (
 id UUID PRIMARY KEY,
 name TEXT NOT NULL,
 phone TEXT NOT NULL,
 email TEXT DEFAULT '',
 appointment_type TEXT NOT NULL CHECK (appointment_type IN ('Video Call','Boutique Visit')),
 appointment_date DATE NOT NULL,
 appointment_time TIME NOT NULL,
 message TEXT DEFAULT '',
 status TEXT NOT NULL DEFAULT 'Pending',
 notification_description TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_products_category_active ON products(category, active);
CREATE INDEX IF NOT EXISTS idx_appointments_date ON appointments(appointment_date);
CREATE INDEX IF NOT EXISTS idx_appointments_status ON appointments(status);
