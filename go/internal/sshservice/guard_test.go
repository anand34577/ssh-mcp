package sshservice

import (
	"strings"
	"testing"

	"example.com/ssh-mcp-server/internal/config"
)

// The same cases are in the Java SystemGuardTest; keep them in step.

var guardBlockedCommands = []string{
	"rm -rf /",
	"rm -rf /*",
	"rm -fr / --no-preserve-root",
	"rm --no-preserve-root -rf /tmp",
	"rm -r -f /etc",
	"rm -rf /etc/",
	"rm -Rf /usr/*",
	"rm --recursive /var",
	"rm -rf /home/alice",
	"rm -rf ~",
	"rm -rf ~/",
	"rm -rf ~/*",
	"rm -rf $HOME",
	"rm -rf ${HOME}/*",
	"rm -rf /e*",
	"rm -rf /etc/../",
	"/bin/rm -rf /boot",
	"sudo rm -rf /",
	"sudo -u root rm -rf /",
	"sudo -n env FOO=1 nice -n 5 rm -rf /",
	"timeout 30 rm -rf /",
	"cd /tmp && rm -rf /",
	"echo hi; rm -rf /",
	"ls | xargs rm -rf /",
	`sh -c "rm -rf /"`,
	`bash -lc 'rm -rf /*'`,
	`r'm' -rf "/"`,
	`\rm -rf /`,
	"echo $(rm -rf /)",
	"find / -delete",
	"find /etc -name '*.conf' -delete",
	"find /var -type f -exec rm {} +",
	"chmod -R 777 /",
	"chown -R nobody /etc",
	"chgrp --recursive staff /usr",
	"mv /etc /tmp/etc",
	"mv ~/notes.txt /dev/null",
	"dd if=/dev/zero of=/dev/sda bs=1M",
	"dd if=image.iso of=/dev/nvme0n1",
	"echo x > /dev/sda",
	"cat junk >/dev/mmcblk0",
	"mkfs.ext4 /dev/sdb1",
	"mkfs -t xfs /dev/sdc",
	"wipefs -a /dev/sda",
	"shred -n 3 /dev/sda",
	"shutdown -h now",
	"sudo reboot",
	"poweroff",
	"systemctl poweroff",
	"sudo systemctl reboot",
	"init 0",
	":(){ :|:& };:",
}

var guardAllowedCommands = []string{
	"ls -la /",
	"df -h /",
	"rm -rf /tmp/build",
	"rm -rf /var/www/html/old",
	"rm -rf /var/log/app/*.gz",
	"rm -rf ~/project/node_modules",
	"rm -rf ./dist",
	"rm /tmp/file.txt",
	"rm -f /etc/nginx/sites-enabled/default",
	"find /tmp -name '*.tmp' -delete",
	"find / -name nginx.conf",
	"chmod 755 /usr/local/bin/tool",
	"chmod -R 755 /var/www/html",
	"chown -R www-data /srv/app/uploads",
	"mv /tmp/a /tmp/b",
	"dd if=/dev/zero of=/tmp/disk.img bs=1M count=10",
	"dd if=/dev/sda of=/dev/null count=1",
	"echo test > /dev/null",
	"systemctl restart nginx",
	"systemctl status --no-pager sshd",
	"git commit -m 'fix reboot loop'",
	"journalctl -b -1 -n 100 --no-pager",
	"grep -r shutdown /var/log/syslog",
	"docker rm -f web",
	"kubectl delete pod web-1",
	"apt-get install -y htop",
	"last reboot",
}

func TestCommandViolationBlocksDangerousCommands(t *testing.T) {
	for _, command := range guardBlockedCommands {
		if commandViolation(command) == "" {
			t.Errorf("expected %q to be blocked", command)
		}
	}
}

func TestCommandViolationAllowsEverydayCommands(t *testing.T) {
	for _, command := range guardAllowedCommands {
		if reason := commandViolation(command); reason != "" {
			t.Errorf("expected %q to be allowed, got %q", command, reason)
		}
	}
}

func TestPathViolation(t *testing.T) {
	cases := []struct {
		path    string
		write   bool
		blocked bool
	}{
		{"/", false, true},
		{"/etc", false, true},
		{"/etc/", false, true},
		{"/home/alice", false, true},
		{".", false, true},
		{"/etc/passwd", true, true},
		{"/etc/shadow", false, true},
		{"/etc/sudoers", true, true},
		{"/boot/grub/grub.cfg", true, true},
		{"/dev/sda", true, true},
		{"/tmp/../etc/fstab", true, true},
		{"/etc/nginx/nginx.conf", true, false},
		{"/etc/nginx/nginx.conf", false, false},
		{"/home/alice/notes.txt", false, false},
		{"/boot/grub/grub.cfg", false, false},
		{"notes.txt", true, false},
		{"/tmp/x", false, false},
	}
	for _, c := range cases {
		if got := pathViolation(c.path, c.write) != ""; got != c.blocked {
			t.Errorf("pathViolation(%q, write=%v) blocked=%v, want %v", c.path, c.write, got, c.blocked)
		}
	}
}

func TestProtectSystemCanBeTurnedOff(t *testing.T) {
	on := &config.ServerConfig{ID: "web", ProtectSystem: true}
	err := checkCommandPolicy(on, "rm -rf /")
	if err == nil || !strings.Contains(err.Error(), `"protectSystem": false for server "web"`) {
		t.Fatalf("expected a system protection error naming the setting, got %v", err)
	}
	if err := checkPathGuard(on, "sftp_delete", "/etc", false); err == nil {
		t.Fatal("expected sftp_delete of /etc to be blocked")
	}

	off := &config.ServerConfig{ID: "web", ProtectSystem: false}
	if err := checkCommandPolicy(off, "rm -rf /"); err != nil {
		t.Fatalf("expected no system protection when protectSystem is false, got %v", err)
	}
	if err := checkPathGuard(off, "sftp_delete", "/etc", false); err != nil {
		t.Fatalf("expected no path protection when protectSystem is false, got %v", err)
	}
}

func TestProtectSystemDefaultsOn(t *testing.T) {
	parse := func(extra string) *config.ServerConfig {
		t.Helper()
		catalog, err := config.Parse([]byte(`{"servers":[{"id":"a","host":"h","username":"u","passwordEnv":"P","strictHostKeyChecking":false` + extra + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		return catalog.Servers["a"]
	}
	t.Setenv(config.EnvAllowInsecureHostKeys, "true")
	if !parse("").ProtectSystem {
		t.Fatal("protectSystem should default to true")
	}
	if parse(`,"protectSystem":false`).ProtectSystem {
		t.Fatal("protectSystem:false should turn the guard off")
	}
}
