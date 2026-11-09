// Package catalog validates catalog objects and moves them in and out of YAML,
// the reviewable form of the catalog.
package catalog

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// The catalog stores where a secret lives, never the secret. These guards are the
// last line behind that rule: they reject what looks like a value, so a slip in a
// YAML file or an agent call fails loudly instead of landing in backups and
// exports.
var (
	secretKey = regexp.MustCompile(`(?i)(^|_)(password|passwd|pwd|secret|private_?key|api_?key|access_?key|token|cookie|passphrase|recovery_?code|otp_?seed)($|_)`)
	// Fields that name a reference rather than hold a value.
	refKeys = map[string]bool{"token_ref": true, "secret_ref": true, "key_ref": true, "password_ref": true}
	slugRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,80}$`)
)

// CheckText rejects a string that contains a secret value.
func CheckText(where, s string) error {
	if model.LooksSecret(s) {
		return fmt.Errorf("%s 看起来包含秘密值，目录只保存引用", where)
	}
	return nil
}

func checkValue(where string, v any) error {
	switch x := v.(type) {
	case string:
		return CheckText(where, x)
	case []any:
		for i, it := range x {
			if err := checkValue(fmt.Sprintf("%s[%d]", where, i), it); err != nil {
				return err
			}
		}
	case map[string]any:
		return CheckAttrs(where, x)
	case map[any]any:
		// YAML yields this for a mapping with non-string keys, which the checks
		// below could not see into. Nothing in the catalog needs one.
		return fmt.Errorf("%s 的键必须是字符串", where)
	}
	return nil
}

// CheckAttrs rejects secret-looking field names with a value, and secret-looking
// values under any name.
func CheckAttrs(where string, attrs map[string]any) error {
	for k, v := range attrs {
		if secretKey.MatchString(k) && !refKeys[k] {
			if s, ok := v.(string); !ok || s != "" {
				return fmt.Errorf("%s.%s 这个字段名表示秘密，目录只保存引用", where, k)
			}
		}
		if err := checkValue(where+"."+k, v); err != nil {
			return err
		}
	}
	return nil
}

// Validate checks an entity before it is written.
func Validate(e *model.Entity) error {
	if !slices.Contains(model.Kinds, e.Kind) {
		return fmt.Errorf("未知对象类型 %q", e.Kind)
	}
	if !slugRe.MatchString(e.Slug) {
		return fmt.Errorf("slug %q 只能使用小写字母、数字和 . _ -", e.Slug)
	}
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("%s 缺少名称", e.Slug)
	}
	if e.Lifecycle != "" && !slices.Contains(model.Lifecycles, e.Lifecycle) {
		return fmt.Errorf("未知生命周期 %q", e.Lifecycle)
	}
	if e.Kind == model.KindProject {
		if st, ok := e.Attrs["stage"].(string); ok && st != "" && !slices.Contains(model.ProjectStages, st) {
			return fmt.Errorf("未知项目阶段 %q", st)
		}
	}
	for _, f := range []struct{ w, v string }{{"name", e.Name}, {"summary", e.Summary}} {
		if err := CheckText(e.Slug+"."+f.w, f.v); err != nil {
			return err
		}
	}
	for _, t := range e.Tags {
		if err := CheckText(e.Slug+".tags", t); err != nil {
			return err
		}
	}
	return CheckAttrs(e.Slug, e.Attrs)
}
