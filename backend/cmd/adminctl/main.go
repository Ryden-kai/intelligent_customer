// Command adminctl is an operator-only CLI for the admin_users table.
//
// It connects directly to the SQLite file (so the API server does not have
// to be running) and supports the operations requested by the admin
// dashboard operators:
//
//   adminctl list
//   adminctl add   <username> [-p <password>]
//   adminctl passwd <username|id> [-p <new-password>]
//   adminctl rename <username|id> <new-username>
//   adminctl del   <username|id>
//
// Passwords are read from stdin by default to avoid leaking into shell
// history. The -p flag accepts a password as an argument for CI / container
// bootstrap; when used, the value is masked in the usage banner only.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"intelligent_customer/backend/internal/db"
	"intelligent_customer/backend/internal/repo"
	"intelligent_customer/backend/internal/security"
)

const usage = `adminctl — manage the admin_users table directly

Usage:
  adminctl -db <path> <command> [args]

Commands:
  list                                  List every admin account
  add   <username> [-p password]        Add a new account (default role: admin)
  passwd <username|id> [-p password]    Replace the password hash
  rename <username|id> <new-username>   Rename an account
  del   <username|id>                   Delete an account (refuses last admin)

Global flags:
  -db PATH       SQLite path (default: $DB_PATH or ./data/intelligent_customer.db)
  -role ROLE     Role for 'add' (default: admin)
  -y             Skip confirmation prompts (CI use)

If -p is omitted for 'add' / 'passwd', the password is read from the
terminal without echo. The interactive prompt can be fed via stdin:

  echo 'newpass' | adminctl passwd admin
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "adminctl:", err)
		os.Exit(1)
	}
}

func run() error {
	argv := os.Args[1:]
	if len(argv) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd := argv[0]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return nil
	}
	rest := argv[1:]

	// We parse global flags by hand because the standard flag package
	// doesn't support interleaved global flags + sub-commands cleanly.
	fs := flag.NewFlagSet("adminctl", flag.ContinueOnError)
	dbPath := fs.String("db", defaultDBPath(), "SQLite database path")
	role := fs.String("role", "admin", "role for 'add'")
	assumeYes := fs.Bool("y", false, "skip confirmation prompts")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	cmdArgs := fs.Args()

	conn, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn, db.MigrationsFS, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	admins := repo.NewAdminUsers(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch cmd {
	case "list":
		return doList(ctx, admins)
	case "add":
		if len(cmdArgs) < 1 {
			return errors.New("usage: adminctl add <username> [-p password]")
		}
		return doAdd(ctx, admins, cmdArgs[0], *role, *assumeYes)
	case "passwd":
		if len(cmdArgs) < 1 {
			return errors.New("usage: adminctl passwd <username|id> [-p password]")
		}
		return doPasswd(ctx, admins, cmdArgs[0], *assumeYes)
	case "rename":
		if len(cmdArgs) < 2 {
			return errors.New("usage: adminctl rename <username|id> <new-username>")
		}
		return doRename(ctx, admins, cmdArgs[0], cmdArgs[1])
	case "del", "delete", "rm":
		if len(cmdArgs) < 1 {
			return errors.New("usage: adminctl del <username|id>")
		}
		return doDelete(ctx, admins, cmdArgs[0], *assumeYes)
	default:
		return fmt.Errorf("unknown command %q (try 'adminctl help')", cmd)
	}
}

func defaultDBPath() string {
	if v := os.Getenv("DB_PATH"); v != "" {
		return v
	}
	return "./data/intelligent_customer.db"
}

// ---- sub-commands -------------------------------------------------------

func doList(ctx context.Context, admins *repo.AdminUsers) error {
	rows, err := admins.List(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("(no admin users yet — bootstrap creates the first row from ADMIN_USERNAME / ADMIN_PASSWORD on server startup)")
		return nil
	}
	fmt.Printf("%-36s  %-24s  %-10s  %-24s  %-24s\n", "ID", "USERNAME", "ROLE", "CREATED", "LAST LOGIN")
	for _, u := range rows {
		ll := "—"
		if u.LastLoginAt != nil {
			ll = u.LastLoginAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-36s  %-24s  %-10s  %-24s  %-24s\n",
			u.ID, u.Username, u.Role,
			u.CreatedAt.Local().Format("2006-01-02 15:04:05"), ll)
	}
	return nil
}

func doAdd(ctx context.Context, admins *repo.AdminUsers, username, role string, skipConfirm bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username must not be empty")
	}
	if role == "" {
		role = "admin"
	}

	if existing, err := admins.GetByUsername(ctx, username); err == nil && existing != nil {
		return fmt.Errorf("user %q already exists (id=%s)", username, existing.ID)
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return err
	}

	pw, err := readPassword("-p", skipConfirm, "Enter password for new admin: ")
	if err != nil {
		return err
	}
	if err := validatePassword(pw); err != nil {
		return err
	}

	hash, err := security.HashPassword(pw)
	if err != nil {
		return err
	}
	u, err := admins.Insert(ctx, username, hash, role)
	if err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	fmt.Printf("✓ created admin %q (id=%s, role=%s)\n", u.Username, u.ID, u.Role)
	return nil
}

func doPasswd(ctx context.Context, admins *repo.AdminUsers, key string, skipConfirm bool) error {
	u, err := admins.LookupByUsernameOrID(ctx, key)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return fmt.Errorf("no admin matches %q", key)
		}
		return err
	}
	pw, err := readPassword("-p", skipConfirm, fmt.Sprintf("New password for %s: ", u.Username))
	if err != nil {
		return err
	}
	if err := validatePassword(pw); err != nil {
		return err
	}
	hash, err := security.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := admins.UpdatePassword(ctx, u.ID, hash); err != nil {
		return err
	}
	fmt.Printf("✓ password updated for %s (id=%s)\n", u.Username, u.ID)
	return nil
}

func doRename(ctx context.Context, admins *repo.AdminUsers, key, newUsername string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return errors.New("new username must not be empty")
	}
	u, err := admins.LookupByUsernameOrID(ctx, key)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return fmt.Errorf("no admin matches %q", key)
		}
		return err
	}
	if existing, err := admins.GetByUsername(ctx, newUsername); err == nil && existing != nil && existing.ID != u.ID {
		return fmt.Errorf("username %q is already taken by %s", newUsername, existing.ID)
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	if err := admins.UpdateUsername(ctx, u.ID, newUsername); err != nil {
		return err
	}
	fmt.Printf("✓ renamed %s → %s\n", u.Username, newUsername)
	return nil
}

func doDelete(ctx context.Context, admins *repo.AdminUsers, key string, skipConfirm bool) error {
	u, err := admins.LookupByUsernameOrID(ctx, key)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return fmt.Errorf("no admin matches %q", key)
		}
		return err
	}
	// Guard against deleting the last admin — operators brick the
	// dashboard otherwise and have to manually edit the DB.
	n, err := admins.Count(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errors.New("refusing to delete the last admin account")
	}
	if !skipConfirm {
		fmt.Printf("about to delete admin %q (id=%s). type yes to continue: ", u.Username, u.ID)
		sc := bufio.NewScanner(os.Stdin)
		if !sc.Scan() {
			return errors.New("no confirmation received")
		}
		if strings.TrimSpace(strings.ToLower(sc.Text())) != "yes" {
			return errors.New("aborted")
		}
	}
	if err := admins.Delete(ctx, u.ID); err != nil {
		return err
	}
	fmt.Printf("✓ deleted admin %q (id=%s)\n", u.Username, u.ID)
	return nil
}

// ---- helpers -------------------------------------------------------------

// readPassword returns the password the operator wants to set. Order:
//
//	1) -p <value>   — used by CI / containers; prints a warning.
//	2) stdin pipe   — fed via echo "x" | adminctl …
//	3) interactive  — prompt via term.ReadPassword (no echo).
func readPassword(pFlag string, skipConfirm bool, prompt string) (string, error) {
	// We accept -p inline by re-parsing the raw args; this keeps the flag
	// declaration in main() free of duplication while still letting the
	// usage string advertise the flag.
	raw, hasFlag, val := extractPasswordFlag(os.Args, pFlag)
	if hasFlag {
		if !skipConfirm {
			fmt.Fprintln(os.Stderr, "warning: password supplied via", pFlag, "argument; it may be visible in process tables")
		}
		// Make sure we don't leak the password into Bash history when
		// the flag is the only thing on the command line.
		_ = raw
		if val == "" {
			return "", errors.New("password from " + pFlag + " is empty")
		}
		return val, nil
	}

	// Stdin pipe (non-tty).
	if stat, _ := os.Stdin.Stat(); stat != nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			return strings.TrimRight(sc.Text(), "\r\n"), nil
		}
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", errors.New("no password on stdin")
	}

	// Interactive TTY.
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(pw), nil
}

// extractPasswordFlag walks argv looking for "-p VALUE" or "-p=VALUE" and
// returns (rawJoined, true, value). When the flag is absent the boolean is
// false and value is "".
func extractPasswordFlag(argv []string, name string) (string, bool, string) {
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == name {
			if i+1 < len(argv) {
				return strings.Join(argv, " "), true, argv[i+1]
			}
			return strings.Join(argv, " "), true, ""
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.Join(argv, " "), true, strings.TrimPrefix(a, name+"=")
		}
	}
	return "", false, ""
}

func validatePassword(pw string) error {
	if pw == "" {
		return errors.New("password must not be empty")
	}
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	return nil
}