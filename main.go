package main

import (
	"bufio"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func feedbackOnErr(err error) {
	if err != nil {
		// point towards https://github.com/luisferreira32/liet/issues with a template
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		panic("oops, something went wrong...")
	}
}

const (
	configFileEnv = "LIET_CONFIG"
	logLevelEnv   = "LIET_LOG_LEVEL"
	logFileEnv    = "LIET_LOG_FILE"
	debugEnv      = "LIET_DEBUG"
)

func configureLogger() (cleanup func() error, err error) {
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
	err = os.MkdirAll(filepath.Dir(logFile), 0o700)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to create log directory: %w", err))
	}
	f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
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
	stats     bool
	exportCSV string
	importCSV string
	yeet      bool
}

func parse() (arguments, flags) {
	f := flags{}
	flagset := flag.NewFlagSet("liet", flag.ExitOnError)
	flagset.StringVar(&f.comment, "c", "", "Additional context for the transaction")
	flagset.StringVar(&f.date, "d", "", "Date of the transaction (YYYY-MM-DD), defaults to today")
	flagset.BoolVar(&f.stats, "w", false, "This is for when you ask: 'What am I doing with my life?'")
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
	flagset.Parse(os.Args[1:])

	if f.date == "" {
		f.date = time.Now().Format("2006-01-02")
	}
	_, err := time.Parse("2006-01-02", f.date)
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

	if len(args) == 0 && !f.stats && f.exportCSV == "" && f.importCSV == "" && !f.yeet {
		flagset.Usage()
	}

	slog.Debug("Parsed arguments", "arguments", a, "flags", f)

	return a, f
}

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
	b, err := os.ReadFile(configPath)
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

		parts := strings.SplitN(line, "=", 2)
		switch parts[0] {
		case "database":
			if len(parts) < 2 {
				return u, fmt.Errorf("missing value for 'database' in config file %q", configPath)
			}
			databasePath := strings.TrimSpace(parts[1])
			if databasePath == "" {
				return u, fmt.Errorf("empty value for 'database' in config file %q", configPath)
			}
			u.databasePath = databasePath
		default:
		}
	}

	return u, nil
}

type database interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func dbInit(db database) error {
	_, err := db.Query(`
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

func insertTransaction(db database, cost float64, category, comment string, date string) error {
	query := "INSERT INTO transactions (cost, category, comment, date) VALUES (?, ?, ?, ?)"
	_, err := db.Query(query, cost, category, comment, date)
	if err != nil {
		return fmt.Errorf("failed to insert transaction: %w", err)
	}
	return nil
}

type transactionSummary struct {
	category  sql.NullString
	totalCost float64
}

func dbStats(db database) error {
	rows, err := db.Query(`
		SELECT
			category,
			SUM(cost) AS total_cost
		FROM
			transactions
		GROUP BY
			category
		ORDER BY
			category
	`)
	if err != nil {
		return fmt.Errorf("failed to query stats: %w", err)
	}
	defer rows.Close()

	var allTimeSummaries []transactionSummary
	for rows.Next() {
		var s transactionSummary
		if err := rows.Scan(&s.category, &s.totalCost); err != nil {
			log.Fatalf("Error scanning all time row: %v", err)
		}
		allTimeSummaries = append(allTimeSummaries, s)
	}

	if len(allTimeSummaries) == 0 {
		fmt.Println("No transactions found for all time.")
		return nil
	}

	slices.SortFunc(allTimeSummaries, func(a, b transactionSummary) int { return int(a.totalCost - b.totalCost) })
	for _, s := range allTimeSummaries {
		category := "N/A"
		if s.category.Valid {
			category = s.category.String
		}
		fmt.Printf("Cost, Category: %.2f, %s\n", s.totalCost, category)
	}

	return nil
}

func dbExport(db database, filePath string) error {
	rows, err := db.Query("SELECT * FROM transactions")
	if err != nil {
		return fmt.Errorf("failed to query transactions: %w", err)
	}
	defer rows.Close()

	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create export file %q: %w", filePath, err)
	}
	defer f.Close()

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

	return nil
}

func dbImport(db database, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open import file %q: %w", filePath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var header bool
	for scanner.Scan() {
		line := scanner.Text()
		if !header {
			header = true
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 5 {
			return fmt.Errorf("invalid line in import file: %s", line)
		}
		cost, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return fmt.Errorf("invalid cost value in import file: %s", parts[1])
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
	cleanup, err := configureLogger()
	defer cleanup()
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
	case f.stats:
		err = dbStats(db)
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
		fmt.Println("No action specified. This should not happen.")
	}
}
