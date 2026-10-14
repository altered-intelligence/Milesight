-- Optional TimescaleDB layer (ADR-012). On vanilla Postgres this is a no-op, so
-- dev/CI can run without Timescale; production images get hypertables,
-- compression and retention automatically.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb')
       AND current_setting('shared_preload_libraries', true) LIKE '%timescaledb%' THEN
        CREATE EXTENSION IF NOT EXISTS timescaledb;

        PERFORM create_hypertable('telemetry', 'time',
                                  chunk_time_interval => INTERVAL '7 days',
                                  if_not_exists => TRUE, migrate_data => TRUE);

        ALTER TABLE telemetry SET (timescaledb.compress,
                                   timescaledb.compress_segmentby = 'vehicle_id',
                                   timescaledb.compress_orderby = 'time DESC');
        PERFORM add_compression_policy('telemetry', INTERVAL '7 days', if_not_exists => TRUE);
        PERFORM add_retention_policy('telemetry', INTERVAL '2 years', if_not_exists => TRUE);
        RAISE NOTICE 'milesight: TimescaleDB enabled for telemetry';
    ELSE
        RAISE NOTICE 'milesight: TimescaleDB not available; telemetry stays a plain table';
    END IF;
END
$$;
