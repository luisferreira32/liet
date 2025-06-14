package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// magic numbers.
const (
	daysOfWeek  = 7
	daysOfMonth = 31 // yes, there is also 28, 29 and 30. but we don't care about that here.

	costColWidth = 20
	colPadding   = 2    // for padding column headers
	highCost     = 1e15 // arbitrary high cost for pretty printing
)

type (
	statsFunc    func(db database) error
	statsCommand string
)

func statsHelp(statsMap map[statsCommand]statsFunc) {
	helperMapping := map[statsCommand][2]string{
		"alltime":   {"all-time", "Category-wise cost aggregation for all time"}, //nolint:misspell // this is a sanitized string
		"lastweek":  {"last week", "Category-wise cost aggregation for the last week"},
		"lastmonth": {"last month", "Category-wise cost aggregation for the last month"},
		"today":     {"today", "Category-wise cost aggregation for today"},
	}

	fmt.Println("Valid stats commands:")
	for cmd := range statsMap {
		h, exists := helperMapping[cmd]
		if !exists {
			fmt.Printf("- %s (no description available)\n", cmd)
			continue
		}
		fmt.Printf("- '%s' or '%s': %s\n", cmd, h[0], h[1])
	}
}

func statsRunner(db database, stats string) error {
	statsMap := map[statsCommand]statsFunc{
		"alltime":   allTimeCostAggregation, //nolint:misspell // this is a sanitized string
		"today":     todayCostAggregation,
		"week":      thisWeekCostAggregation,
		"month":     thisMonthCostAggregation,
		"lastweek":  lastWeekCostAggregation,
		"lastmonth": lastMonthCostAggregation,
		"monthly":   monthlyCostAggregation,
	}

	s := statsCommand(strings.TrimSpace(strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(stats, "-", ""), " ", ""))))
	if s == "help" || s == "-h" || s == "--help" {
		statsHelp(statsMap)
		return nil
	}
	if statsFunc, ok := statsMap[s]; ok {
		return statsFunc(db)
	}
	fmt.Printf("Unknown stats command: %s, run with -w help to know valid values\n", stats)
	return nil
}

func allTimeCostAggregation(db database) error {
	return costAggregrationTable(db, "all time", "0000-00-00", "9999-12-31")
}

func todayCostAggregation(db database) error {
	now := time.Now()
	startDate := now.Format("2006-01-02")
	endDate := now.AddDate(0, 0, 1).Format("2006-01-02")
	slog.Debug("Today is", "startDate", startDate, "endDate", endDate)
	return costAggregrationTable(db, "today", startDate, endDate)
}

func thisWeekCostAggregation(db database) error {
	now := time.Now()
	startDate := now.AddDate(0, 0, -int(now.Weekday()-1)).Format("2006-01-02")
	endDate := now.AddDate(0, 0, daysOfWeek-int(now.Weekday())).Format("2006-01-02")
	slog.Debug("This week is", "startDate", startDate, "endDate", endDate)
	return costAggregrationTable(db, "this week", startDate, endDate)
}

func thisMonthCostAggregation(db database) error {
	now := time.Now()
	startDate := now.AddDate(0, 0, -now.Day()+1).Format("2006-01-02")
	// does not really matter we use 31, we don't expect to have transactions in the future
	endDate := now.AddDate(0, 1, daysOfMonth-now.Day()).Format("2006-01-02")
	slog.Debug("This month is", "startDate", startDate, "endDate", endDate)
	return costAggregrationTable(db, "this month", startDate, endDate)
}

func lastWeekCostAggregation(db database) error {
	now := time.Now()
	startDate := now.AddDate(0, 0, -int(now.Weekday()-1)-daysOfWeek).Format("2006-01-02")
	endDate := now.AddDate(0, 0, -int(now.Weekday())).Format("2006-01-02")
	slog.Debug("Last week is", "startDate", startDate, "endDate", endDate)
	return costAggregrationTable(db, "last week", startDate, endDate)
}

func lastMonthCostAggregation(db database) error {
	now := time.Now()
	startDate := now.AddDate(0, -1, -now.Day()+1).Format("2006-01-02")
	endDate := now.AddDate(0, 0, -now.Day()).Format("2006-01-02")
	slog.Debug("Last month is", "startDate", startDate, "endDate", endDate)
	return costAggregrationTable(db, "last month", startDate, endDate)
}

func costAggregrationTable(db database, queryType, startDate, endDate string) error {
	allTimeSummaries, err := costAggregration(db, startDate, endDate)
	if err != nil {
		return fmt.Errorf("failed to aggregate costs: %w", err)
	}

	if len(allTimeSummaries) == 0 {
		fmt.Printf("No transactions found for %s.\n", queryType)
		return nil
	}

	maxLen := len(slices.MaxFunc(allTimeSummaries, func(a, b transactionSummary) int {
		return len(a.category.String) - len(b.category.String)
	}).category.String)
	if maxLen < len("Category")+2 {
		maxLen = len("Category") + colPadding
	}

	line := strings.Repeat("-", maxLen+3+costColWidth)
	fmt.Printf(`
%v
|%*s |%19s |
%v
`, line, maxLen-1, "Category", "Cost", line)

	slices.SortFunc(allTimeSummaries, func(a, b transactionSummary) int { return int(a.totalCost - b.totalCost) })
	for _, s := range allTimeSummaries {
		category := "N/A"
		if s.category.Valid {
			category = s.category.String
		}
		if s.totalCost > highCost {
			fmt.Printf("|%*s | %18.10g |\n", maxLen-1, category, s.totalCost)
		} else {
			fmt.Printf("|%*s | %18.2f |\n", maxLen-1, category, s.totalCost)
		}
	}
	fmt.Println(line)

	return nil
}

func monthlyCostAggregation(db database) error {
	now := time.Now()
	expenses := make(map[string][]transactionSummary, 0)
	for m := time.January; m <= now.Month(); m++ {
		startDate := time.Date(now.Year(), m, 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		endDate := time.Date(now.Year(), m+1, 1, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1).Format("2006-01-02")
		slog.Debug("Month", "month", m.String(), "startDate", startDate, "endDate", endDate)
		monthExpenses, err := costAggregration(db, startDate, endDate)
		if err != nil {
			return fmt.Errorf("failed to aggregate costs for month %s: %w", m.String(), err)
		}
		expenses[m.String()] = monthExpenses
	}
	uniqueCategories := map[string]struct{}{}
	for _, monthExpenses := range expenses {
		for _, s := range monthExpenses {
			if s.category.Valid {
				uniqueCategories[s.category.String] = struct{}{}
			} else {
				uniqueCategories["N/A"] = struct{}{}
			}
		}
	}
	maxLen := len("Category") + colPadding
	line := strings.Repeat("-", maxLen+2+(costColWidth+1)*len(expenses))
	costLine := strings.Builder{}
	for m := time.January; m <= now.Month(); m++ {
		costLine.WriteString(fmt.Sprintf("%19s |", m.String()))
	}
	fmt.Printf(`
%v
|%*s |%s
%v
`, line, maxLen-1, "Category", costLine.String(), line)

	for category := range uniqueCategories {
		costLine.Reset()
		for m := time.January; m <= now.Month(); m++ {
			monthExpenses := expenses[m.String()]
			totalCost := 0.0
			for _, s := range monthExpenses {
				if s.category.Valid && s.category.String == category {
					totalCost += s.totalCost
				} else if !s.category.Valid && category == "N/A" {
					totalCost += s.totalCost
				}
			}
			if totalCost > highCost { // completely arbitrary, for the pretty print
				costLine.WriteString(fmt.Sprintf(" %18.10g |", totalCost))
			} else {
				costLine.WriteString(fmt.Sprintf(" %18.2f |", totalCost))
			}
		}
		fmt.Printf("|%*s |%s\n", maxLen-1, category, costLine.String())
	}

	fmt.Println(line)
	return nil
}

type transactionSummary struct {
	category  sql.NullString
	totalCost float64
}

func costAggregration(db database, startDate, endDate string) ([]transactionSummary, error) {
	rows, err := db.Query(`
SELECT
    category,
    SUM(cost) AS total_cost
FROM
    transactions
WHERE
    date BETWEEN ? AND ?  -- Filter by date range
GROUP BY
    category
ORDER BY
    category;
	`, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query stats: %w", err)
	}
	defer handleErrClose(rows.Close)

	var allTimeSummaries []transactionSummary
	for rows.Next() {
		var s transactionSummary
		if err := rows.Scan(&s.category, &s.totalCost); err != nil {
			return nil, fmt.Errorf("error scanning all time row: %w", err)
		}
		allTimeSummaries = append(allTimeSummaries, s)
	}

	if rows.Err() != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", rows.Err())
	}

	return allTimeSummaries, nil
}
