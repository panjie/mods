package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// SettingInfo describes one persisted YAML path for self-help generation.
// It contains only code-owned metadata and safe defaults, never loaded user
// configuration.
type SettingInfo struct {
	Path        string
	ValueType   string
	Description string
	Default     string
}

// SelfHelpSettings returns the persisted config schema used by mods. Top-level
// keys come from PersistentConfig, while map value schemas use placeholders
// matching their actual YAML shape.
func SelfHelpSettings() []SettingInfo {
	defaults := Default()
	settings := settingsForValue("", reflect.ValueOf(defaults.PersistentConfig), true)
	settings = append(settings, settingsForType(
		"mcp-servers.<server>",
		reflect.TypeFor[MCPServerConfig](),
	)...)
	settings = append(settings, settingsForType(
		"apis.<provider>",
		reflect.TypeFor[API](),
	)...)
	settings = append(settings, settingsForType(
		"apis.<provider>.models.<model>",
		reflect.TypeFor[Model](),
	)...)
	slices.SortFunc(settings, func(a, b SettingInfo) int {
		return strings.Compare(a.Path, b.Path)
	})
	return settings
}

func settingsForValue(prefix string, value reflect.Value, topLevel bool) []SettingInfo {
	typ := value.Type()
	settings := make([]SettingInfo, 0, typ.NumField())
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Name == "System" {
			continue
		}
		key, ok := persistedFieldName(field, topLevel)
		if !ok {
			continue
		}
		path := joinSettingPath(prefix, key)
		fieldValue := value.Field(i)
		settings = append(settings, settingInfo(path, field.Type, fieldValue))
		switch field.Type {
		case reflect.TypeFor[PromptConfig](), reflect.TypeFor[BuiltinToolsConfig]():
			settings = append(settings, settingsForValue(path, fieldValue, false)...)
		}
	}
	return settings
}

func settingsForType(prefix string, typ reflect.Type) []SettingInfo {
	settings := make([]SettingInfo, 0, typ.NumField())
	for i := range typ.NumField() {
		field := typ.Field(i)
		key, ok := persistedFieldName(field, false)
		if !ok {
			continue
		}
		path := joinSettingPath(prefix, key)
		settings = append(settings, settingInfo(path, field.Type, reflect.Value{}))
	}
	return settings
}

func persistedFieldName(field reflect.StructField, allowImplicit bool) (string, bool) {
	tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
	if tag == "-" {
		return "", false
	}
	if tag != "" {
		return tag, true
	}
	if !allowImplicit {
		return "", false
	}
	return strings.ToLower(field.Name), true
}

func joinSettingPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func settingInfo(path string, typ reflect.Type, value reflect.Value) SettingInfo {
	description := Help[path]
	if description == "" {
		description = Help[path[strings.LastIndex(path, ".")+1:]]
	}
	return SettingInfo{
		Path:        path,
		ValueType:   settingType(path, typ),
		Description: description,
		Default:     safeDefault(path, typ, value),
	}
}

func settingType(path string, typ reflect.Type) string {
	if typ == reflect.TypeFor[time.Duration]() {
		return "duration"
	}
	if strings.HasPrefix(path, "apis") && typ == reflect.TypeFor[APIs]() {
		return "mapping"
	}
	switch typ.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.String:
		return "string"
	case reflect.Slice:
		return "list"
	case reflect.Map, reflect.Struct:
		return "mapping"
	default:
		return typ.String()
	}
}

func safeDefault(path string, typ reflect.Type, value reflect.Value) string {
	if !value.IsValid() || secretOrOpaqueSetting(path) {
		return ""
	}
	if typ == reflect.TypeFor[time.Duration]() {
		duration := time.Duration(value.Int())
		if duration == 0 {
			return ""
		}
		return duration.String()
	}
	switch typ.Kind() {
	case reflect.Bool:
		return fmt.Sprintf("%t", value.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", value.Int())
	case reflect.String:
		if value.String() == "" {
			return ""
		}
		return value.String()
	default:
		return ""
	}
}

func secretOrOpaqueSetting(path string) bool {
	return path == "web-search-api-key" ||
		strings.HasSuffix(path, ".api-key") ||
		strings.HasSuffix(path, ".api-key-cmd") ||
		strings.HasPrefix(path, "prompts.") ||
		path == "format-text" ||
		path == "roles" ||
		strings.HasSuffix(path, ".extra-params") ||
		strings.HasSuffix(path, ".env")
}
