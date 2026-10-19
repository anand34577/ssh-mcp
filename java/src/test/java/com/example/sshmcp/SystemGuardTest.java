package com.example.sshmcp;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;

// The same cases are in the Go guard_test.go; keep them in step.
class SystemGuardTest {
    private static final String[] BLOCKED = {
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
            "sh -c \"rm -rf /\"",
            "bash -lc 'rm -rf /*'",
            "r'm' -rf \"/\"",
            "\\rm -rf /",
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
    };

    private static final String[] ALLOWED = {
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
    };

    private static final String[][] PATHS = {
            {"/", "false", "true"},
            {"/etc", "false", "true"},
            {"/etc/", "false", "true"},
            {"/home/alice", "false", "true"},
            {".", "false", "true"},
            {"/etc/passwd", "true", "true"},
            {"/etc/shadow", "false", "true"},
            {"/etc/sudoers", "true", "true"},
            {"/boot/grub/grub.cfg", "true", "true"},
            {"/dev/sda", "true", "true"},
            {"/tmp/../etc/fstab", "true", "true"},
            {"/etc/nginx/nginx.conf", "true", "false"},
            {"/etc/nginx/nginx.conf", "false", "false"},
            {"/home/alice/notes.txt", "false", "false"},
            {"/boot/grub/grub.cfg", "false", "false"},
            {"notes.txt", "true", "false"},
            {"/tmp/x", "false", "false"},
    };

    @Test
    void blocksDangerousCommands() {
        for (String command : BLOCKED) {
            assertNotNull(SystemGuard.commandViolation(command), "expected to be blocked: " + command);
        }
    }

    @Test
    void allowsEverydayCommands() {
        for (String command : ALLOWED) {
            assertNull(SystemGuard.commandViolation(command), "expected to be allowed: " + command);
        }
    }

    @Test
    void guardsSftpPaths() {
        for (String[] row : PATHS) {
            boolean blocked = SystemGuard.pathViolation(row[0], Boolean.parseBoolean(row[1])) != null;
            assertEquals(Boolean.parseBoolean(row[2]), blocked, row[0] + " write=" + row[1]);
        }
    }

    @Test
    void cleansPathsLikeGo() {
        assertEquals("/", SystemGuard.clean("/etc/../"));
        assertEquals("/etc", SystemGuard.clean("//etc/./"));
        assertEquals(".", SystemGuard.clean(""));
        assertEquals("../a", SystemGuard.clean("../a"));
        assertEquals("/", SystemGuard.clean("/.."));
    }
}
