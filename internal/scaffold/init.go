package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

const defaultInfra = `domain acme {
  cloud      = aws(region: us-east-1)
  owner      = team(platform)

  boundary {
    budget   = 5000/mo
    auto     = [scale, disk_expand, rotate_creds, restart]
    approve  = [instance_type_change, new_store, expose_public]
    forbid   = [delete_store, open_0_0_0_0, disable_encryption]
  }

  compliance = [soc2, hipaa]
}

network core {
  topology = vpc
}

store postgres {
  engine = postgres
}

service api {
  runtime = container(from: ./Dockerfile)
  expose  = public(port: 443)

  performance {
    latency = p95 < 200ms
    uptime  = 99.9%
  }

  needs {
    postgres = read_write
  }
}
`

func Init(dir string) (string, error) {
	path := filepath.Join(dir, "infra.beecon")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	if err := os.WriteFile(path, []byte(defaultInfra), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
