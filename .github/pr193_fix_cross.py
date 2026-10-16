from pathlib import Path

path = Path("scripts/crosscompile/coverage_security_test.go")
text = path.read_text()
old = '{filter: buildTargetFilter{goarch: "arm64"}, want: 6},'
new = '{filter: buildTargetFilter{goarch: "arm64"}, want: 5},'
if text.count(old) != 1:
    raise RuntimeError(f"arm64 target count: expected one match, got {text.count(old)}")
path.write_text(text.replace(old, new, 1))
