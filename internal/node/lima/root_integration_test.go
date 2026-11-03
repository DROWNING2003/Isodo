package lima

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	"github.com/DROWNING2003/Isodo/internal/node/guesttransport"
)

func TestLimaGuestRootIntegration(t *testing.T) {
	if os.Getenv("ISODO_LIMA_ROOT_INTEGRATION") != "true" {
		t.Skip("set ISODO_LIMA_ROOT_INTEGRATION=true for isolated real Guest root verification")
	}
	manager, err := NewManager(Config{GuestAgentBinary: os.Getenv("ISODO_GUEST_AGENT_BINARY"), InstancePrefix: "isodo-root-test-", CPUs: 1, MemoryGiB: 1, DiskGiB: 5})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("root-test-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	t.Logf("创建隔离 Lima 沙箱 Sandbox ID=%s", id)
	instance, err := manager.Create(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := manager.Delete(ctx, id); err != nil {
			t.Errorf("清理 %s: %v", id, err)
		} else {
			t.Logf("已删除测试沙箱 %s (%s)", id, instance.Name)
		}
	})
	boot, err := manager.BootID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := manager.GuestEndpoint(id)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := guesttransport.BindRuntimeTCP(ctx, endpoint, &guestv1.RuntimeIdentity{SandboxId: id, OwnershipGeneration: 1, BootId: boot})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := guesttransport.NewTCPClientConn(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	out, exit, err := runGuestCommand(ctx, guestv1.NewGuestServiceClient(conn), ready.GetIdentity(), "root-check", []string{"/bin/sh", "-ec", `test "$(id -u)" = 0; test "$(id -g)" = 0; test "$USER" = root; test "$HOME" = /workspace/.home; touch /etc/isodo-root-test; rm /etc/isodo-root-test; dpkg --audit; printf root-guest-ok`})
	if err != nil || exit != 0 || !strings.Contains(out, "root-guest-ok") {
		t.Fatalf("Guest root 验证 output=%q exit=%d error=%v", out, exit, err)
	}
	t.Logf("Sandbox ID=%s Guest UID/GID=0，系统目录写入及 dpkg 检查通过", id)
}
