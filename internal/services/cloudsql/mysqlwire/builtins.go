package mysqlwire

import (
	"strings"

	"github.com/go-mysql-org/go-mysql/mysql"
)

// tryBuiltin handles a small set of admin/info queries that real MySQL
// clients issue before (and sometimes between) user queries. They don't
// translate cleanly to sqlite and the answer is the same every time, so we
// hand-roll them. Returns (result, handled, error).
func (h *handler) tryBuiltin(query string, binary bool) (*mysql.Result, bool, error) {
	q := strings.TrimSpace(query)
	q = strings.TrimSuffix(q, ";")
	up := strings.ToUpper(q)

	switch {
	case up == "":
		return mysql.NewResultReserveResultset(0), true, nil

	// MySQL drivers issue START TRANSACTION; sqlite wants BEGIN.
	case up == "START TRANSACTION", strings.HasPrefix(up, "START TRANSACTION "):
		r, err := h.runExec("BEGIN", nil)
		return r, true, err

	// Charset / autocommit / mode tweaks: accept, no-op.
	case strings.HasPrefix(up, "SET NAMES"),
		strings.HasPrefix(up, "SET CHARACTER SET"),
		strings.HasPrefix(up, "SET CHARSET"),
		strings.HasPrefix(up, "SET AUTOCOMMIT"),
		strings.HasPrefix(up, "SET SQL_MODE"),
		strings.HasPrefix(up, "SET SESSION"),
		strings.HasPrefix(up, "SET @@"),
		strings.HasPrefix(up, "SET TIME_ZONE"),
		strings.HasPrefix(up, "SET TRANSACTION"),
		strings.HasPrefix(up, "SET WAIT_TIMEOUT"),
		strings.HasPrefix(up, "SET INTERACTIVE_TIMEOUT"):
		return mysql.NewResultReserveResultset(0), true, nil

	case up == "SELECT VERSION()" || up == "SELECT @@VERSION":
		return singleStringResult("VERSION()", "8.0.11-gcp-local", binary)

	case up == "SELECT @@VERSION_COMMENT":
		return singleStringResult("@@version_comment", "gcp-local emulator", binary)

	case up == "SELECT DATABASE()":
		return singleStringResult("DATABASE()", h.database, binary)

	case up == "SELECT CURRENT_USER()" || up == "SELECT USER()":
		return singleStringResult("USER()", DefaultUser+"@localhost", binary)

	case strings.HasPrefix(up, "SHOW VARIABLES"),
		strings.HasPrefix(up, "SHOW SESSION VARIABLES"),
		strings.HasPrefix(up, "SHOW GLOBAL VARIABLES"),
		strings.HasPrefix(up, "SHOW WARNINGS"),
		strings.HasPrefix(up, "SHOW ENGINES"),
		strings.HasPrefix(up, "SHOW STATUS"),
		strings.HasPrefix(up, "SHOW CHARSET"),
		strings.HasPrefix(up, "SHOW COLLATION"):
		// Empty result is fine — drivers tolerate it.
		rs, err := mysql.BuildSimpleResultset([]string{"Variable_name", "Value"}, [][]any{}, binary)
		if err != nil {
			return nil, true, err
		}
		return mysql.NewResult(rs), true, nil

	case strings.HasPrefix(up, "SHOW TABLES"):
		// Defer to the runtime: query sqlite_master with a translated stub.
		return nil, false, nil
	}

	return nil, false, nil
}

func singleStringResult(name, value string, binary bool) (*mysql.Result, bool, error) {
	rs, err := mysql.BuildSimpleResultset([]string{name}, [][]any{{value}}, binary)
	if err != nil {
		return nil, true, err
	}
	return mysql.NewResult(rs), true, nil
}
