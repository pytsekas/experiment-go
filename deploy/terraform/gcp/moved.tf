# State migration from the flat layout that lived in deploy/terraform/ until
# 2026-09. Terraform applies these once; they are harmless afterwards.

moved {
  from = google_project_service.apis
  to   = module.registry.google_project_service.apis
}

moved {
  from = google_artifact_registry_repository.containers
  to   = module.registry.google_artifact_registry_repository.containers
}

moved {
  from = google_sql_database_instance.main[0]
  to   = module.database[0].google_sql_database_instance.main
}

moved {
  from = google_sql_database.app[0]
  to   = module.database[0].google_sql_database.app
}

moved {
  from = google_sql_user.app[0]
  to   = module.database[0].google_sql_user.app
}

moved {
  from = google_cloud_run_v2_service.api[0]
  to   = module.serverless[0].google_cloud_run_v2_service.api
}

moved {
  from = google_cloud_run_v2_service_iam_member.public[0]
  to   = module.serverless[0].google_cloud_run_v2_service_iam_member.public
}

moved {
  from = google_container_cluster.main[0]
  to   = module.kubernetes[0].google_container_cluster.main
}

moved {
  from = google_container_node_pool.spot[0]
  to   = module.kubernetes[0].google_container_node_pool.spot
}

moved {
  from = google_project_iam_member.workload_identity_cloudsql[0]
  to   = module.kubernetes[0].google_project_iam_member.workload_identity_cloudsql
}
