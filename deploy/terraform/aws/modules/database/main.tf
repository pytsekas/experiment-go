# Generated once, stored in Secrets Manager, never written to a tfvars file.
resource "random_password" "db" {
  length  = 32
  special = false # keeps the value safe inside a URL
}

resource "aws_db_subnet_group" "main" {
  name       = "${var.name}-pg"
  subnet_ids = var.subnet_ids

  tags = { Name = "${var.name}-pg" }
}

# No ingress rules here: each consumer (ECS tasks, EKS nodes) adds its own
# aws_vpc_security_group_ingress_rule, which avoids a cycle between toggled modules.
resource "aws_security_group" "db" {
  name        = "${var.name}-db"
  description = "Postgres, ingress granted per consumer"
  vpc_id      = var.vpc_id

  tags = { Name = "${var.name}-db" }
}

# The cheapest useful shape: burstable, single AZ, no backups. The default
# parameter group for Postgres 15+ forces TLS (rds.force_ssl = 1), so clients
# must connect with sslmode=require.
resource "aws_db_instance" "main" {
  identifier     = "${var.name}-pg"
  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = "db.t4g.micro"

  allocated_storage = 20
  storage_type      = "gp3"

  db_name  = var.db_name
  username = var.db_user
  password = random_password.db.result

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.db.id]
  publicly_accessible    = false
  multi_az               = false

  backup_retention_period = 0
  skip_final_snapshot     = true
  deletion_protection     = false
  apply_immediately       = true

  tags = { Name = "${var.name}-pg" }
}

resource "aws_secretsmanager_secret" "db_password" {
  name                    = "${var.name}-db-password"
  recovery_window_in_days = 0 # delete immediately so teardown + recreate works

  tags = { Name = "${var.name}-db-password" }
}

resource "aws_secretsmanager_secret_version" "db_password" {
  secret_id     = aws_secretsmanager_secret.db_password.id
  secret_string = random_password.db.result
}
