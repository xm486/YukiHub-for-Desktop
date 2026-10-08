package importpath

import "testing"

// Windows 路径必须在任何宿主平台上得到一致结果（导入 Windows 备份时，
// Linux 上也要生成相同的去重键与保留路径）。
func TestNormalizeWindowsPathsHostIndependently(t *testing.T) {
	cases := map[string]string{
		`D:\Galgame\a.exe`:      `d:\galgame\a.exe`,
		`D:\Galgame//Sub\a.exe`: `d:\galgame\sub\a.exe`,
		`d:/Galgame/Sub/a.exe`:  `d:\galgame\sub\a.exe`,
		`D:\Galgame\Sub\`:       `d:\galgame\sub`,
		`\\SERVER\Share\Game\a`: `\\server\share\game\a`,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinWindowsKeepsCaseAndSeparators(t *testing.T) {
	if got := JoinWindows(`D:\Games\Mixed`, "start.exe"); got != `D:\Games\Mixed\start.exe` {
		t.Errorf("JoinWindows relative = %q", got)
	}
	if got := JoinWindows(`D:\Games\Mixed`, `D:\Other\run.exe`); got != `D:\Other\run.exe` {
		t.Errorf("JoinWindows absolute = %q", got)
	}
	if got := JoinWindows(`\\server\share\`, "game/run.exe"); got != `\\server\share\game\run.exe` {
		t.Errorf("JoinWindows UNC = %q", got)
	}
}

func TestIsWindowsAbs(t *testing.T) {
	for _, path := range []string{`D:\a`, `d:/a`, `\\server\share`} {
		if !IsWindowsAbs(path) {
			t.Errorf("IsWindowsAbs(%q) = false, want true", path)
		}
	}
	for _, path := range []string{`/home/u/a`, `D:a`, `relative\a`} {
		if IsWindowsAbs(path) {
			t.Errorf("IsWindowsAbs(%q) = true, want false", path)
		}
	}
}

func TestConflictsWindowsPaths(t *testing.T) {
	if !Conflicts(`D:\Games\A`, `d:/games/a/start.exe`) {
		t.Error("expected nested Windows paths to conflict")
	}
	if Conflicts(`D:\Games\A`, `D:\Games\B`) {
		t.Error("unrelated Windows paths must not conflict")
	}
}

// "." / ".." 必须被解析掉，否则同一路径的两种写法会被判成两个游戏。
// 这是 filepath.Clean 顺带做过、而跨平台的 Windows 路径实现容易丢掉的一环。
func TestNormalizeWindowsResolvesDotSegments(t *testing.T) {
	cases := map[string]string{
		`D:\Games\..\Other\a.exe`: `d:\other\a.exe`,
		`D:\Games\.\a.exe`:        `d:\games\a.exe`,
		`d:\games\sub\..\..\`:     `d:\`,
		`D:\Games\sub\..\`:        `d:\games`,
		`D:\Games\..\..\..\x`:     `d:\x`,
		`D:\Games\Sub\..\.\a`:     `d:\games\a`,
		`D:/Games/../Other/a.exe`: `d:\other\a.exe`,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// UNC 的 `\\server\share` 根不能被 ".." 吃掉。
func TestNormalizeWindowsUNCRootIsStable(t *testing.T) {
	cases := map[string]string{
		`\\SERVER\Share\Game\..\a.exe`: `\\server\share\a.exe`,
		`\\SERVER\Share`:               `\\server\share`,
		`\\SERVER\Share\`:              `\\server\share`,
		`\\SERVER\share\..\..\a.exe`:   `\\server\share\a.exe`,
		`\\server`:                     `\\server`,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// 解析出 ".." 之后必须真的被判成同一路径 —— 这才是重复检测要的结果。
func TestDotSegmentPathsConflictAfterNormalize(t *testing.T) {
	if !Conflicts(`D:\Games\..\Other`, `D:\Other`) {
		t.Error(`D:\Games\..\Other 与 D:\Other 应判为同一路径`)
	}
	if !Conflicts(`D:\Games\.\A`, `d:/games/a/start.exe`) {
		t.Error(`D:\Games\.\A 与 d:/games/a/start.exe 应判为冲突`)
	}
}
