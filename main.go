package main

import (
	"bufio"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	version string // to be set by the build system.
	errUser = errors.New("you messed up")
)

func recoverFeedback() {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "%v", r)
		os.Exit(1)
	}
}

func feedbackOnErr(err error) {
	if errors.Is(err, errUser) {
		panic(err.Error())
	}
	if err != nil {
		b, _ := debug.ReadBuildInfo()
		fmt.Fprintf(os.Stderr, `
During execution the following error was found:

	[ERROR] %v

If you really cannot solve it yourself, feel free to open a bug issue at: https://github.com/luisferreira32/liet/issues
The issue must be prefixed with [BUG] and some descriptive title, e.g.:

	[BUG] database coult not be wiped

The issue description must contain:

[ERROR] %v

L version: %v
[DEBUG] build info:

%v
`, err, err, version, b)
		panic("oops, something went wrong...\n")
	}
}

func handleErrClose(f func() error) {
	err := f()
	if err != nil {
		slog.Error("Failed to close resource", "error", err)
	}
}

const (
	configFileEnv = "LIET_CONFIG"
	logLevelEnv   = "LIET_LOG_LEVEL"
	logFileEnv    = "LIET_LOG_FILE"
	debugEnv      = "LIET_DEBUG"
)

func configureLogger() (func() error, error) {
	var (
		w        io.Writer
		errs     []error
		logLevel = os.Getenv(logLevelEnv)
		logFile  = os.Getenv(logFileEnv)
		debug    = os.Getenv(debugEnv) != ""
	)

	if logFile == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get home directory: %w", err))
		}
		logFile = filepath.Join(homeDir, defaultLogFile)
	}
	err := os.MkdirAll(filepath.Dir(logFile), 0o700) //nolint:mnd // reasonable dir permissions
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to create log directory: %w", err))
	}
	f, err := os.OpenFile(filepath.Clean(logFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) //nolint:mnd // reasonable file permissions
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to open log file %q: %w", logFile, err))
	}
	w = f

	l, err := strconv.Atoi(logLevel)
	if err != nil && logLevel != "" {
		errs = append(errs, fmt.Errorf("invalid log level: %w", err))
	}

	if debug {
		w = os.Stderr
	}

	if len(errs) > 0 {
		return f.Close, errors.Join(errs...)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.Level(l), // default is InfoLevel = 0
	})))

	return f.Close, nil
}

type arguments struct {
	cost     float64
	category string
}

type flags struct {
	comment   string
	date      string
	stats     string
	exportCSV string
	importCSV string
	yeet      bool
}

func parse() (arguments, flags) {
	f := flags{}
	flagset := flag.NewFlagSet("liet", flag.ExitOnError)
	flagset.StringVar(&f.comment, "c", "", "Additional context for the transaction")
	flagset.StringVar(&f.date, "d", "", "Date of the transaction (YYYY-MM-DD), defaults to today")
	flagset.StringVar(&f.stats, "w", "", `This is for when you ask: What am I doing with my life?
Normal values can be: "last week", "last month", "all time" or "today". For an exaustive list run with -w help.`)
	flagset.StringVar(&f.exportCSV, "e", "", "Export transactions to a file (CSV format)")
	flagset.StringVar(&f.importCSV, "i", "", "Import transactions from a file (CSV format) replacing any current data")
	flagset.BoolVar(&f.yeet, "yeet", false, "Remove all known user data of the application: database, logs, configs (use with caution!)")
	flagset.Usage = func() {
		fmt.Printf("Usage: %s [<cost> [<category>] [<flags>] | <flags>]\n", os.Args[0])
		flagset.PrintDefaults()
		fmt.Printf("\nExamples:\n")
		fmt.Printf("  %s 10.50 groceries\n", os.Args[0])
		fmt.Printf("  %s 9.6 -c 'Bought some stuff' -d 2023-10-01\n", os.Args[0])
		fmt.Printf("  %s -w\n", os.Args[0])
		fmt.Printf("  %s -e transactions.csv\n", os.Args[0])
		fmt.Printf("  %s -i import.csv\n", os.Args[0])
		fmt.Printf("  %s -yeet\n", os.Args[0])
		os.Exit(1)
	}
	err := flagset.Parse(os.Args[1:])
	if err != nil {
		panic(fmt.Errorf("oops, something went wrong... failed to parse flags: %w", err))
	}

	if f.date == "" {
		f.date = time.Now().Format("2006-01-02")
	}
	_, err = time.Parse("2006-01-02", f.date)
	if err != nil {
		fmt.Printf("Invalid date format: %v, expecting YYYY-MM-DD.\nerr:%v\n\n", f.date, err)
		flagset.Usage()
	}

	a := arguments{}
	args := flagset.Args()
	slog.Debug("Parsing arguments...", "args", args)
	// liet <cost> [<category>] [<flags>]
	if len(args) > 0 {
		var err error
		a.cost, err = strconv.ParseFloat(args[0], 64)
		if err != nil {
			fmt.Printf("Invalid cost value: %v, expecting a number.\nerr:%v\n\n", args[0], err)
			flagset.Usage()
		}
	}
	if len(args) > 1 {
		a.category = args[1]
	}

	slog.Debug("Parsed arguments", "arguments", a, "flags", f)

	return a, f
}

const (
	keyValuePairs = 2
)

type userConfig struct {
	databasePath string
}

func loadUserConfig() (userConfig, error) {
	configPath := os.Getenv(configFileEnv)
	u := userConfig{}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return u, fmt.Errorf("failed to get home directory: %w", err)
	}

	if configPath == "" {
		configPath = filepath.Join(homeDir, defaultConfigFile)
		slog.Debug("No config file specified, using default location", "path", configPath)
	}
	b, err := os.ReadFile(filepath.Clean(configPath))
	if errors.Is(err, os.ErrNotExist) {
		u = userConfig{
			databasePath: filepath.Join(homeDir, defaultDatabaseFile),
		}
		slog.Debug("No config file found, using default database config", "path", filepath.Join(homeDir, defaultDatabaseFile))
		return u, nil
	}
	if err != nil {
		return u, fmt.Errorf("failed to read config file %q: %w", configPath, err)
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue // skip empty lines and comments
		}

		parts := strings.SplitN(line, "=", keyValuePairs)
		switch parts[0] {
		case "database":
			if len(parts) < keyValuePairs {
				return u, fmt.Errorf("%w: missing value for 'database' in config file %q", errUser, configPath)
			}
			databasePath := strings.TrimSpace(parts[1])
			if databasePath == "" {
				return u, fmt.Errorf("%w: empty value for 'database' in config file %q", errUser, configPath)
			}
			u.databasePath = databasePath
		default:
		}
	}

	return u, nil
}

type database interface {
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

func dbInit(db database) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cost REAL NOT NULL,
			category TEXT,
			comment TEXT,
			date TEXT NOT NULL
	);
	`)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	return nil
}

func insertTransaction(db database, cost float64, category, comment, date string) error {
	query := "INSERT INTO transactions (cost, category, comment, date) VALUES (?, ?, ?, ?)"
	categoryPtr := sql.NullString{String: category, Valid: strings.TrimSpace(category) != ""}
	_, err := db.Exec(query, cost, categoryPtr, comment, date)
	if err != nil {
		return fmt.Errorf("failed to insert transaction: %w", err)
	}
	return nil
}

func dbExport(db database, filePath string) error {
	rows, err := db.Query("SELECT * FROM transactions")
	if err != nil {
		return fmt.Errorf("failed to query transactions: %w", err)
	}
	defer handleErrClose(rows.Close)

	f, err := os.Create(filepath.Clean(filePath))
	if err != nil {
		return fmt.Errorf("failed to create export file %q: %w", filePath, err)
	}
	defer handleErrClose(f.Close)

	if _, err := f.WriteString("id,cost,category,comment,date\n"); err != nil {
		return fmt.Errorf("failed to write to export file: %w", err)
	}

	for rows.Next() {
		var id int
		var cost float64
		var category, comment, date string
		if err := rows.Scan(&id, &cost, &category, &comment, &date); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		line := fmt.Sprintf("%d,%.2f,%s,%s,%s\n", id, cost, category, comment, date)
		if _, err := f.WriteString(line); err != nil {
			return fmt.Errorf("failed to write to export file: %w", err)
		}
	}
	if rows.Err() != nil {
		return fmt.Errorf("error iterating over rows: %w", rows.Err())
	}

	return nil
}

func dbImport(db database, filePath string) error {
	f, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		return fmt.Errorf("failed to open import file %q: %w", filePath, err)
	}
	defer handleErrClose(f.Close)

	scanner := bufio.NewScanner(f)
	var header bool
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !header {
			header = true
			continue
		}
		lineNum++
		parts := strings.Split(line, ",")
		if len(parts) < 5 { //nolint:mnd // until someone properly implements the CSV import, I'll just leave this hardcoded
			return fmt.Errorf("%w: invalid line import file %s, line %d: %s", errUser, filePath, lineNum, line)
		}
		cost, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return fmt.Errorf("%w: invalid cost value in import file %s, line %d: %s", errUser, filePath, lineNum, parts[1])
		}
		category := parts[2]
		comment := parts[3]
		date := parts[4]

		err = insertTransaction(db, cost, category, comment, date)
		if err != nil {
			return fmt.Errorf("failed to insert transaction from import file: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading import file: %w", err)
	}

	return nil
}

func confirmYeet(confirmationQuestion string) bool {
	fmt.Print(confirmationQuestion)
	var confirmation string
	_, err := fmt.Scanln(&confirmation)
	if err != nil {
		fmt.Printf("Failed to read confirmation input: %v\n", err)
		return false
	}
	return confirmation == "yes"
}

func yeet(databasePath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	ok := confirmYeet(fmt.Sprintf("Are you sure you want to wipe the database at %q?\nType 'yes' to confirm: ", databasePath))
	if !ok {
		fmt.Println("Operation cancelled.")
		return nil
	}
	err = os.Remove(databasePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove database file %q: %w", databasePath, err)
	}
	fmt.Println("Database wiped successfully.")

	configPath := os.Getenv(configFileEnv)
	if configPath == "" {
		configPath = filepath.Join(homeDir, defaultConfigFile)
	}
	ok = confirmYeet(fmt.Sprintf("Are you sure you want to wipe the config file at %q?\nType 'yes' to confirm: ", configPath))
	if !ok {
		fmt.Println("Operation cancelled.")
		return nil
	}
	err = os.Remove(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove config file %q: %w", configPath, err)
	}
	fmt.Println("Config file wiped successfully.")

	logFile := os.Getenv(logFileEnv)
	if logFile == "" {
		logFile = filepath.Join(homeDir, defaultLogFile)
	}
	ok = confirmYeet(fmt.Sprintf("Are you sure you want to wipe the log file at %q?\nType 'yes' to confirm: ", logFile))
	if !ok {
		fmt.Println("Operation cancelled.")
		return nil
	}
	err = os.Remove(logFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove log file %q: %w", logFile, err)
	}
	fmt.Println("Log file wiped successfully.")
	fmt.Println("All specified user data has been wiped successfully.")
	return nil
}

func main() {
	defer recoverFeedback()

	cleanup, err := configureLogger()
	defer handleErrClose(cleanup)
	feedbackOnErr(err)

	a, f := parse()
	c, err := loadUserConfig()
	feedbackOnErr(err)

	db, err := sql.Open("sqlite", c.databasePath)
	feedbackOnErr(err)
	err = dbInit(db)
	feedbackOnErr(err)

	switch {
	case a.cost != 0:
		err = insertTransaction(db, a.cost, a.category, f.comment, f.date)
		feedbackOnErr(err)
	case f.stats != "":
		err = statsRunner(db, f.stats)
		feedbackOnErr(err)
	case f.exportCSV != "":
		err = dbExport(db, f.exportCSV)
		feedbackOnErr(err)
	case f.importCSV != "":
		err = dbImport(db, f.importCSV)
		feedbackOnErr(err)
	case f.yeet:
		err = yeet(c.databasePath)
		feedbackOnErr(err)
	default:
		fmt.Println("I don't think you wanted to end up here... How about running with -h for help?")
	}
}
