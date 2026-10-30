variable "image" {
  type = string
}

variable "node_name" {
  type = string
}

variable "bind_addr" {
  type = string
}

variable "api_port" {
  type = number
}

variable "config_dir" {
  type = string
}

job "isodo-api-pr18-test" {
  datacenters = ["dc1"]
  type        = "service"

  group "control" {
    count = 1

    constraint {
      attribute = "${node.unique.name}"
      value     = var.node_name
    }

    network {
      mode = "host"

      port "api" {
        static = var.api_port
      }
    }

    update {
      max_parallel      = 1
      health_check      = "checks"
      min_healthy_time  = "5s"
      healthy_deadline  = "90s"
      progress_deadline = "3m"
      auto_revert       = true
    }

    task "api" {
      driver = "docker"

      config {
        image        = var.image
        network_mode = "host"
        force_pull   = true
        entrypoint   = ["/bin/sh"]
        args = [
          "-ec",
          "set -a; . /run/isodo/api.env; set +a; unset ISODO_NODE_RPC_TARGET ISODO_CONTROL_TLS_CERT ISODO_CONTROL_TLS_KEY ISODO_NODE_SERVER_CA ISODO_NODE_TLS_SERVER_NAME ISODO_NODE_ID ISODO_NODE_BACKEND; export ISODO_LISTEN_ADDR='${var.bind_addr}:${var.api_port}'; export ISODO_INJECTION_KEY_FILE=/run/isodo/certs/injection.key; exec /usr/local/bin/isodo-api",
        ]
        volumes = [
          "${var.config_dir}/api.env:/run/isodo/api.env:ro",
          "${var.config_dir}/certs:/run/isodo/certs:ro",
        ]
      }

      resources {
        cpu    = 500
        memory = 512
      }

      service {
        provider = "nomad"
        name     = "isodo-api-pr18-test"
        port     = "api"

        check {
          name         = "api-health"
          type         = "http"
          path         = "/healthz"
          interval     = "10s"
          timeout      = "3s"
          address_mode = "host"
        }
      }
    }
  }
}
