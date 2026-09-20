# google_cloud_run_v2_service_iam_member.public gained a count so a second
# instance of this module (the ingest role) can opt out of public access via
# allow_public_access. Every existing caller defaults to true, so this move
# keeps the already-applied resource in place instead of destroying and
# recreating it under its new indexed address.
moved {
  from = google_cloud_run_v2_service_iam_member.public
  to   = google_cloud_run_v2_service_iam_member.public[0]
}

# google_service_account.run gained a count so a caller can supply its own
# service_account_email instead of getting one created here. Every existing
# caller leaves service_account_email empty, so this move keeps the
# already-applied service account in place under its new indexed address.
moved {
  from = google_service_account.run
  to   = google_service_account.run[0]
}

# google_project_iam_member.run_cloudsql gained a count so a database-free
# caller (the ingest role) isn't granted a database role it will never use.
# Every existing caller defaults requires_database to true, so this move
# keeps the already-applied binding in place under its new indexed address.
moved {
  from = google_project_iam_member.run_cloudsql
  to   = google_project_iam_member.run_cloudsql[0]
}
