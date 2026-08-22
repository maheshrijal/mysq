CREATE USER 'mysq_monitor'@'%' IDENTIFIED BY 'mysq-monitor-test';
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'mysq_monitor'@'%';
GRANT SELECT ON performance_schema.* TO 'mysq_monitor'@'%';
GRANT SELECT ON app.* TO 'mysq_monitor'@'%';

CREATE USER 'loadgen'@'%' IDENTIFIED BY 'mysq-load-test';
GRANT ALL PRIVILEGES ON app.* TO 'loadgen'@'%';

USE app;

CREATE TABLE accounts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(255) NOT NULL,
  balance DECIMAL(14,2) NOT NULL DEFAULT 0,
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY ux_accounts_email (email)
) ENGINE=InnoDB;

CREATE TABLE orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  account_id BIGINT UNSIGNED NOT NULL,
  status VARCHAR(32) NOT NULL,
  amount DECIMAL(12,2) NOT NULL,
  payload JSON NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY idx_orders_account_created (account_id, created_at),
  KEY idx_orders_status (status),
  KEY idx_orders_status_duplicate (status),
  CONSTRAINT fk_orders_account FOREIGN KEY (account_id) REFERENCES accounts(id)
) ENGINE=InnoDB;

-- Deliberately lacks a primary key so the end-to-end run proves schema findings.
CREATE TABLE audit_events (
  account_id BIGINT UNSIGNED NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  message VARCHAR(512) NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  KEY idx_audit_created (created_at)
) ENGINE=InnoDB;
