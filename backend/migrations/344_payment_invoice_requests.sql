CREATE TABLE IF NOT EXISTS payment_invoice_requests (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    user_email VARCHAR(255) NOT NULL,
    order_ids JSONB NOT NULL,
    amount DECIMAL(20,2) NOT NULL CHECK (amount > 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    buyer_type VARCHAR(20) NOT NULL CHECK (buyer_type IN ('personal', 'company')),
    title VARCHAR(200) NOT NULL,
    tax_id VARCHAR(30) NOT NULL DEFAULT '',
    emails JSONB NOT NULL,
    remark VARCHAR(2000) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'issued', 'rejected')),
    admin_remark VARCHAR(2000) NOT NULL DEFAULT '',
    invoice_number VARCHAR(100) NOT NULL DEFAULT '',
    file_name VARCHAR(255) NOT NULL DEFAULT '',
    pdf_data BYTEA,
    issued_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS invoicerequest_user_id_created_at ON payment_invoice_requests (user_id, created_at);
CREATE INDEX IF NOT EXISTS invoicerequest_status_created_at ON payment_invoice_requests (status, created_at);

ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS invoice_request_id BIGINT REFERENCES payment_invoice_requests(id);
CREATE INDEX IF NOT EXISTS paymentorder_invoice_request_id ON payment_orders (invoice_request_id) WHERE invoice_request_id IS NOT NULL;
