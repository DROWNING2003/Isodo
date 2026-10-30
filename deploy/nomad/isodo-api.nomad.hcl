variable "image" { type = string }
variable "node_name" { type = string }
job "isodo-api" {
  datacenters = ["dc1"]
  type = "service"
  group "control" {
    count = 1
    constraint { attribute = "${node.unique.name}" value = var.node_name }
    network { mode = "host" port "api" { static = 8080 } }
    update {
      max_parallel = 1
      health_check = "checks"
      min_healthy_time = "5s"
      healthy_deadline = "90s"
      progress_deadline = "3m"
      auto_revert = true
    }
    task "api" {
      driver = "docker"
      config {
        image = var.image
        network_mode = "host"
        force_pull = true
        entrypoint = ["/bin/sh"]
        args = ["-ec", "set -a; . /etc/isodo/api.env; exec /usr/local/bin/isodo-api"]
        volumes = ["/etc/isodo/api.env:/etc/isodo/api.env:ro", "/etc/isodo/certs:/etc/isodo/certs:ro"]
      }
      resources { cpu = 500 memory = 512 }
      service {
        provider = "nomad"
        name = "isodo-api"
        port = "api"
        check { type = "http" path = "/health" interval = "10s" timeout = "3s" }
      }
    }
  }
}
