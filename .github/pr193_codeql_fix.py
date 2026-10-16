from pathlib import Path


def function_span(text, name):
    markers = [f"func {name}(", f"func (a *App) {name}("]
    starts = [text.find(marker) for marker in markers]
    starts = [start for start in starts if start >= 0]
    if not starts:
        raise RuntimeError(f"function not found: {name}")
    start = min(starts)
    opening = text.find(" {\n", start)
    if opening < 0:
        raise RuntimeError(f"function body not found: {name}")
    opening += 1
    depth = 0
    quote = None
    escaped = False
    for index in range(opening, len(text)):
        ch = text[index]
        if quote:
            if quote != "`" and escaped:
                escaped = False
                continue
            if quote != "`" and ch == "\\":
                escaped = True
                continue
            if ch == quote:
                quote = None
            continue
        if ch in ('"', "'", "`"):
            quote = ch
            continue
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return start, index + 1
    raise RuntimeError(f"unterminated function: {name}")


def replace_function(text, name, replacement):
    start, end = function_span(text, name)
    return text[:start] + replacement.rstrip() + text[end:]


def replace_once(text, old, new, label):
    count = text.count(old)
    if count != 1:
        raise RuntimeError(f"{label}: expected one match, got {count}")
    return text.replace(old, new, 1)


site_path = Path("sitebrush.go")
site = site_path.read_text()
site = replace_function(site, "migrateLegacyAnalytics", r'''func (a *App) migrateLegacyAnalytics(database *sql.DB, domain, directory string) error {
    domain = strings.TrimSpace(domain)
    safeDomain := filepath.Base(domain)
    if domain == "" || strings.ContainsAny(domain, "\x00\r\n/\\") || safeDomain != domain || safeDomain == "." || safeDomain == ".." {
        return errors.New("unsafe legacy analytics domain")
    }

    // Request data is used only to select a directory entry that was actually
    // returned by the filesystem. It never becomes a path component itself.
    analyticsRoot := filepath.Join(a.storageRootDir(), "analytics")
    rootEntries, err := os.ReadDir(analyticsRoot)
    if errors.Is(err, os.ErrNotExist) {
        return nil
    }
    if err != nil {
        return err
    }
    expectedDirectoryName := "site-" + url.PathEscape(safeDomain)
    legacyDirectory := ""
    for _, entry := range rootEntries {
        if entry.Name() != expectedDirectoryName {
            continue
        }
        if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
            return errors.New("unsafe legacy analytics directory")
        }
        legacyDirectory = filepath.Join(analyticsRoot, entry.Name())
        break
    }
    if legacyDirectory == "" {
        return nil
    }

    entries, err := os.ReadDir(legacyDirectory)
    if err != nil {
        return err
    }
    expectedDatabaseName := browserstats.DatabaseName(safeDomain)
    namedPath := ""
    fallbackPath := ""
    for _, entry := range entries {
        if entry.Name() != expectedDatabaseName && entry.Name() != "analytics.db" {
            continue
        }
        if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
            return errors.New("unsafe legacy analytics database")
        }
        info, infoErr := entry.Info()
        if infoErr != nil || !info.Mode().IsRegular() {
            return errors.New("unsafe legacy analytics database")
        }
        candidate := filepath.Join(legacyDirectory, entry.Name())
        if entry.Name() == expectedDatabaseName {
            namedPath = candidate
        } else {
            fallbackPath = candidate
        }
    }
    legacyPath := namedPath
    if legacyPath == "" {
        legacyPath = fallbackPath
    }
    if legacyPath == "" {
        return nil
    }

    if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS analytics_legacy_import(domain TEXT PRIMARY KEY)`); err != nil {
        return err
    }
    var imported int
    if err := database.QueryRow(`SELECT COUNT(*) FROM analytics_legacy_import WHERE domain=?`, domain).Scan(&imported); err != nil {
        return err
    }
    if imported == 0 {
        if _, err := database.Exec(`ATTACH DATABASE ? AS legacy_observations`, legacyPath); err != nil {
            return err
        }
        defer database.Exec(`DETACH DATABASE legacy_observations`)
        var integrity string
        if err := database.QueryRow(`PRAGMA legacy_observations.quick_check`).Scan(&integrity); err != nil || integrity != "ok" {
            return errors.New("invalid legacy analytics database")
        }
        transaction, err := database.Begin()
        if err != nil {
            return err
        }
        defer transaction.Rollback()
        if _, err = transaction.Exec(`CREATE TABLE IF NOT EXISTS checkpoints(kind TEXT PRIMARY KEY,body TEXT NOT NULL)`); err != nil {
            return err
        }
        if _, err = transaction.Exec(`INSERT INTO checkpoints(kind,body) SELECT legacy.kind,legacy.body FROM legacy_observations.checkpoints legacy WHERE NOT EXISTS(SELECT 1 FROM main.checkpoints current WHERE current.kind=legacy.kind)`); err != nil {
            return err
        }
        if _, err = transaction.Exec(`INSERT INTO analytics_legacy_import(domain) VALUES(?)`, domain); err != nil {
            return err
        }
        if err = transaction.Commit(); err != nil {
            return err
        }
    }
    sourceArchives := filepath.Join(legacyDirectory, "archives")
    if info, err := os.Lstat(sourceArchives); err == nil {
        if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
            return errors.New("unsafe legacy analytics archives")
        }
        destination := filepath.Join(directory, "archives")
        if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
            if err := os.Rename(sourceArchives, destination); err != nil {
                return err
            }
        } else {
            return errors.New("analytics archive migration destination already exists")
        }
    }
    // The committed import marker prevents stale observations from replacing
    // current state if interruption occurs during source cleanup.
    if _, err := database.Exec(`DETACH DATABASE legacy_observations`); err != nil && imported == 0 {
        return err
    }
    return os.Remove(legacyPath)
}''')
site_path.write_text(site)

store_path = Path("pkg/analytics/store.go")
store = store_path.read_text()
marker = "func storeDirectory(root, domain string) (string, error) {"
helper = r'''func analyticsDatabasePath(directory, domain string) (string, error) {
    entries, err := os.ReadDir(directory)
    if err != nil {
        return "", err
    }
    expectedName := DatabaseName(domain)
    fallback := ""
    for _, entry := range entries {
        if entry.Name() != expectedName && entry.Name() != "analytics.db" {
            continue
        }
        if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
            return "", errors.New("unsafe analytics database")
        }
        info, infoErr := entry.Info()
        if infoErr != nil || !info.Mode().IsRegular() {
            return "", errors.New("unsafe analytics database")
        }
        candidate := filepath.Join(directory, entry.Name())
        if entry.Name() == expectedName {
            return candidate, nil
        }
        fallback = candidate
    }
    if fallback != "" {
        return fallback, nil
    }
    return filepath.Join(directory, "analytics.db"), nil
}

'''
if "func analyticsDatabasePath(" not in store:
    store = replace_once(store, marker, helper + marker, "analytics database path helper")

old_cached = '''\t\t\t\tif cachedPreferences {
\t\t\t\t\t_, databaseErr := os.Stat(filepath.Join(directory, DatabaseName(request.Domain)))
\t\t\t\t\tcachedPreferences = databaseErr == nil
\t\t\t\t}'''
new_cached = '''\t\t\t\tif cachedPreferences {
\t\t\t\t\tdatabasePath, databasePathErr := analyticsDatabasePath(directory, request.Domain)
\t\t\t\t\tif databasePathErr != nil {
\t\t\t\t\t\tcachedPreferences = false
\t\t\t\t\t} else {
\t\t\t\t\t\t_, databaseErr := os.Stat(databasePath)
\t\t\t\t\t\tcachedPreferences = databaseErr == nil
\t\t\t\t\t}
\t\t\t\t}'''
store = replace_once(store, old_cached, new_cached, "cached preferences path")

old_path = '''\tdatabasePath := filepath.Join(directory, DatabaseName(request.Domain))
\tif owned != nil {
\t\tdatabasePath = filepath.Join(directory, "analytics.db")
\t}
\tlegacyPath := filepath.Join(directory, "analytics.db")
\tif _, pathErr := os.Lstat(databasePath); owned == nil && os.IsNotExist(pathErr) {
\t\tif legacy, legacyErr := os.Lstat(legacyPath); legacyErr == nil && legacy.Mode().IsRegular() {
\t\t\tif renameErr := os.Rename(legacyPath, databasePath); renameErr != nil {
\t\t\t\treturn StorageResult{Err: renameErr}
\t\t\t}
\t\t}
\t}'''
new_path = '''\tdatabasePath := filepath.Join(directory, "analytics.db")
\tif owned == nil {
\t\tdatabasePath, err = analyticsDatabasePath(directory, request.Domain)
\t\tif err != nil {
\t\t\treturn StorageResult{Err: err}
\t\t}
\t}'''
store = replace_once(store, old_path, new_path, "database path selection")
store_path.write_text(store)
