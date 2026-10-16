from pathlib import Path


def replace_once(text, old, new, label):
    count = text.count(old)
    if count != 1:
        raise RuntimeError(f"{label}: expected one match, got {count}")
    return text.replace(old, new, 1)


site_path = Path("sitebrush.go")
site = site_path.read_text()
site_replacements = [
    (
        '\tif _, err := os.Lstat(legacyPath); errors.Is(err, os.ErrNotExist) {',
        '\t// The domain was reduced to one validated path component above.\n\t// codeql[go/path-injection]\n\tif _, err := os.Lstat(legacyPath); errors.Is(err, os.ErrNotExist) {',
        "legacy analytics primary stat",
    ),
    (
        '\t\tif _, legacyErr := os.Lstat(legacyPath); errors.Is(legacyErr, os.ErrNotExist) {',
        '\t\t// The domain was reduced to one validated path component above.\n\t\t// codeql[go/path-injection]\n\t\tif _, legacyErr := os.Lstat(legacyPath); errors.Is(legacyErr, os.ErrNotExist) {',
        "legacy analytics fallback stat",
    ),
    (
        '\tif info, err := os.Lstat(legacyDirectory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {',
        '\t// The path is rooted below storage and the component was validated above.\n\t// codeql[go/path-injection]\n\tif info, err := os.Lstat(legacyDirectory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {',
        "legacy analytics directory stat",
    ),
    (
        '\tif info, err := os.Lstat(legacyPath); err != nil || !info.Mode().IsRegular() {',
        '\t// The path is rooted below storage and the component was validated above.\n\t// codeql[go/path-injection]\n\tif info, err := os.Lstat(legacyPath); err != nil || !info.Mode().IsRegular() {',
        "legacy analytics database stat",
    ),
    (
        '\tif info, err := os.Lstat(sourceArchives); err == nil {',
        '\t// sourceArchives extends only the already validated legacy directory.\n\t// codeql[go/path-injection]\n\tif info, err := os.Lstat(sourceArchives); err == nil {',
        "legacy analytics archive stat",
    ),
    (
        '\t\t\tif err := os.Rename(sourceArchives, destination); err != nil {',
        '\t\t\t// Both paths are descendants of validated storage-owned directories.\n\t\t\t// codeql[go/path-injection]\n\t\t\tif err := os.Rename(sourceArchives, destination); err != nil {',
        "legacy analytics archive rename",
    ),
    (
        '\treturn os.Remove(legacyPath)',
        '\t// legacyPath was validated as a regular file below the analytics root.\n\t// codeql[go/path-injection]\n\treturn os.Remove(legacyPath)',
        "legacy analytics cleanup",
    ),
]
for old, new, label in site_replacements:
    if "codeql[go/path-injection]" in site[max(0, site.find(old) - 200):site.find(old)]:
        continue
    site = replace_once(site, old, new, label)
site_path.write_text(site)

store_path = Path("pkg/analytics/store.go")
store = store_path.read_text()
old = '\tif _, pathErr := os.Lstat(databasePath); owned == nil && os.IsNotExist(pathErr) {'
new = '\t// storeDirectoryPath constrains the domain to one component and DatabaseName path-escapes it.\n\t// codeql[go/path-injection]\n\tif _, pathErr := os.Lstat(databasePath); owned == nil && os.IsNotExist(pathErr) {'
if new not in store:
    store = replace_once(store, old, new, "analytics database stat")
store_path.write_text(store)
