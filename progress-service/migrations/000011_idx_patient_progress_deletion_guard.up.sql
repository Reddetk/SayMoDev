BEGIN;
CREATE INDEX IF NOT EXISTS ix_patient_progress_program_version_status
    ON patient_progress (program_id, program_version, status);
COMMIT;