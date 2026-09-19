package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ConfigManager 统一管理所有配置
type ConfigManager struct {
	configs map[string]interface{}
	mutex   sync.RWMutex
}

// fieldMu guards the fields of the structs registered through Register —
// cm.mutex only guards the registry map itself, which says nothing about the
// objects in it.
//
// The registered structs are live: relay goroutines read them per request
// (model_setting.GetGeminiSafetySetting, ClaudeSettings.WriteHeaders,
// GetGlobalSettings().PassThroughRequestEnabled, the fetch_setting SSRF
// lists), while the option-sync tick rewrites them every SYNC_FREQUENCY
// seconds from the options table. Writers take fieldMu for writing and
// publish freshly decoded values (see applyConfigMap; the one other writer is
// the nil repair in ratio_setting.GetGroupRatioSetting, which no production
// path reaches and which takes the write lock); readers that must be
// race-free take it for reading through RLock/RUnlock.
//
// Lock order where both are held: cm.mutex first, then fieldMu (LoadFromDB,
// SaveToDB and ExportAllConfigs are the three places that hold both). A
// pointee that carries its own lock (types.RWMap) is taken last, inside
// fieldMu, and never takes fieldMu itself.
//
// # No nesting
//
// sync.RWMutex is NOT reentrant: once a writer is waiting, a second RLock on
// the same goroutine blocks behind it while the writer blocks behind the first
// RLock — a deadlock, not a slowdown. So an accessor that holds the
// configuration read lock must not call another accessor that takes it. Each
// of the accessors below therefore reads its own fields and returns; the three
// that can repair what they read (model_setting.GetClaudeSettings,
// ratio_setting.GetGroupRatioSetting, operation_setting.GetMonitorSetting)
// release the read lock and then call a helper that takes the write lock and
// re-checks — republishClaudeDefaultMaxTokens, repairGroupSpecialUsableGroup
// and applyMonitorOverride, which are the only three callers of Lock.
// TestConfigLockCallersAreTheKnownSet in config_cow_test.go pins every call
// site so a new one is a deliberate edit rather than a silent addition.
var fieldMu sync.RWMutex

// RLock acquires the configuration read lock. Accessors in the setting
// subpackages that a relay goroutine calls use it so their reads are ordered
// against the option-sync tick's writes; it is exported because those
// accessors live in other packages (model_setting, system_setting).
// Every RLock must be paired with an RUnlock, nothing that holds it may call
// back into a function that writes configuration, and nothing that holds it
// may call a second function that takes it (see "No nesting" above).
func RLock() { fieldMu.RLock() }

// RUnlock releases the configuration read lock taken by RLock.
func RUnlock() { fieldMu.RUnlock() }

// Lock acquires the configuration write lock. It is for a setting package
// publishing a replacement value into its own registered struct — the
// copy-on-write rule still holds, so the caller assigns a freshly built
// map/slice rather than writing into the one already published. The caller
// must not already hold the read lock.
// The three callers today are the repair helpers named above
// (`grep -rn "config.Lock()" --include=*.go .` — claude.go, group_ratio.go,
// monitor_setting.go).
func Lock() { fieldMu.Lock() }

// Unlock releases the configuration write lock taken by Lock.
func Unlock() { fieldMu.Unlock() }

var GlobalConfig = NewConfigManager()

func NewConfigManager() *ConfigManager {
	return &ConfigManager{
		configs: make(map[string]interface{}),
	}
}

// Register 注册一个配置模块
func (cm *ConfigManager) Register(name string, config interface{}) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.configs[name] = config
}

// Get 获取指定配置模块
func (cm *ConfigManager) Get(name string) interface{} {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.configs[name]
}

// LoadFromDB 从数据库加载配置
func (cm *ConfigManager) LoadFromDB(options map[string]string) error {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	for name, config := range cm.configs {
		prefix := name + "."
		configMap := make(map[string]string)

		// 收集属于此配置的所有选项
		for key, value := range options {
			if strings.HasPrefix(key, prefix) {
				configKey := strings.TrimPrefix(key, prefix)
				configMap[configKey] = value
			}
		}

		// 如果找到配置项，则更新配置
		if len(configMap) > 0 {
			// The parseable fields of this module are applied either way;
			// applyConfigMap reports the ones that were not, which are left at
			// their previous values instead of being zeroed.
			if err := applyConfigMap(config, configMap); err != nil {
				common.SysError("failed to update config " + name + ": " + err.Error())
				continue
			}
		}
	}

	return nil
}

// SaveToDB 将配置保存到数据库
func (cm *ConfigManager) SaveToDB(updateFunc func(key, value string) error) error {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	for name, config := range cm.configs {
		configMap, err := configToMap(config)
		if err != nil {
			return err
		}

		for key, value := range configMap {
			dbKey := name + "." + key
			if err := updateFunc(dbKey, value); err != nil {
				return err
			}
		}
	}

	return nil
}

// 辅助函数：将配置对象转换为map
//
// Reads the same live fields applyConfigMap writes, so it takes the read lock:
// without it, serialising a registered config for /api/option while the
// option-sync tick republishes it is the same unguarded read the accessors
// were fixed to stop doing.
func configToMap(config interface{}) (map[string]string, error) {
	fieldMu.RLock()
	defer fieldMu.RUnlock()

	result := make(map[string]string)

	val := reflect.ValueOf(config)
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil, nil
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// 跳过未导出字段
		if !fieldType.IsExported() {
			continue
		}

		// 获取json标签作为键名
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}

		// 处理不同类型的字段
		var strValue string
		switch field.Kind() {
		case reflect.String:
			strValue = field.String()
		case reflect.Bool:
			strValue = strconv.FormatBool(field.Bool())
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			strValue = strconv.FormatInt(field.Int(), 10)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			strValue = strconv.FormatUint(field.Uint(), 10)
		case reflect.Float32, reflect.Float64:
			strValue = strconv.FormatFloat(field.Float(), 'f', -1, 64)
		case reflect.Pointer:
			// 处理指针类型：如果非 nil，序列化指向的值
			if !field.IsNil() {
				bytes, err := json.Marshal(field.Interface())
				if err != nil {
					return nil, err
				}
				strValue = string(bytes)
			} else {
				// nil 指针序列化为 "null"
				strValue = "null"
			}
		case reflect.Map, reflect.Slice, reflect.Struct:
			// 复杂类型使用JSON序列化
			bytes, err := json.Marshal(field.Interface())
			if err != nil {
				return nil, err
			}
			strValue = string(bytes)
		default:
			// 跳过不支持的类型
			continue
		}

		result[key] = strValue
	}

	return result, nil
}

// applyConfigMap 从 map 更新配置对象，采用 copy-on-write。
//
// Two properties this function is responsible for, both of them reachable
// from a single admin edit or from any option-sync tick:
//
//  1. It does not write into a map, slice or pointee that has already been
//     published: each value is decoded into a fresh allocation and then
//     published with one Set. encoding/json, handed the address of a live
//     field, merges into an existing map and re-appends into an existing
//     slice's backing array — so a reader ranging over the map met the
//     runtime's unrecoverable "concurrent map read and map write", and a
//     reader holding the slice header evaluated a mixture of the old and new
//     lists that nobody had published (the SSRF domain list is read exactly
//     that way, app/ssrf_guard.go).
//
//  2. A value that does not parse leaves its field at the previous value and
//     is reported, instead of being skipped silently. The fields that do
//     parse are still applied — one bad key in a module must not block the
//     rest of that module's settings.
//
// The one exception to "publish a fresh allocation" is a non-nil pointer field
// whose type unmarshals itself (types.RWMap, the type of
// group_ratio_setting.group_special_usable_group): replacing that pointer
// would write the pointer word on every update, and it is read with no lock
// on the token-auth path (app.GetUserUsableGroups, reached per request from
// middleware/auth.go). Such a field is unmarshalled INTO the existing pointee,
// which takes the pointee's own lock — the pointer word is written once, in
// the package's init, and never again. The value is validated into a throwaway
// instance first, because RWMap.UnmarshalJSON empties itself before decoding,
// so handing it malformed JSON directly would publish an empty map.
//
// The decode phase touches nothing; the publish phase takes fieldMu once for
// the whole struct, so a reader under RLock sees this update's fields together
// rather than interleaved with its own read.
func applyConfigMap(config interface{}, configMap map[string]string) error {
	val := reflect.ValueOf(config)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return nil
	}
	val = val.Elem()

	if val.Kind() != reflect.Struct {
		return nil
	}

	// A pendingField either publishes a freshly decoded value (value) or
	// unmarshals raw into the pointee already published (inPlace).
	type pendingField struct {
		index   int
		value   reflect.Value
		raw     string
		inPlace bool
	}

	var (
		pending []pendingField
		errs    []error
	)

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// 跳过未导出字段
		if !fieldType.IsExported() {
			continue
		}

		// 获取json标签作为键名
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}

		// 检查map中是否有对应的值
		strValue, ok := configMap[key]
		if !ok {
			continue
		}

		// 根据字段类型设置值
		if !field.CanSet() {
			continue
		}

		if selfUnmarshalingPointee(field) {
			// Validate into a throwaway of the same type; the live pointee is
			// only touched in the publish phase below.
			probe := reflect.New(field.Type().Elem())
			if err := json.Unmarshal([]byte(strValue), probe.Interface()); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, parseKindError(field.Type())))
				continue
			}
			pending = append(pending, pendingField{index: i, raw: strValue, inPlace: true})
			continue
		}

		next, err := decodeConfigValue(field.Type(), strValue)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		if !next.IsValid() {
			// 不支持的字段类型：与旧实现一致，静默跳过
			continue
		}
		pending = append(pending, pendingField{index: i, value: next})
	}

	if len(pending) > 0 {
		fieldMu.Lock()
		for _, p := range pending {
			if p.inPlace {
				// The pointee takes its own lock for the whole decode
				// (types.RWMap.UnmarshalJSON), so a concurrent reader of that
				// map waits rather than observing it half-filled. The value
				// was validated above, so an error here is not reachable from
				// a stored string; it is collected rather than ignored so a
				// future self-unmarshaling type cannot fail silently.
				if err := json.Unmarshal([]byte(p.raw), val.Field(p.index).Interface()); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", typ.Field(p.index).Name, parseKindError(val.Field(p.index).Type())))
				}
				continue
			}
			val.Field(p.index).Set(p.value)
		}
		fieldMu.Unlock()
	}

	return errors.Join(errs...)
}

// selfUnmarshalingPointee reports whether field is a non-nil pointer whose
// type decodes itself through json.Unmarshaler. The only such field in a
// registered config today is
// ratio_setting.GroupRatioSetting.GroupSpecialUsableGroup
// (*types.RWMap[string, map[string]string]).
// TestRegisteredPointerFieldsDecodeInPlace, in
// internal/app/group_ratio_race_test.go (this package's own test binary does
// not link the packages that register the configs, so the enumeration has to
// live above them), walks the registered structs and fails if a pointer field
// appears that would not take this path.
func selfUnmarshalingPointee(field reflect.Value) bool {
	if field.Kind() != reflect.Pointer || field.IsNil() {
		return false
	}
	return field.Type().Implements(jsonUnmarshalerType)
}

var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// decodeConfigValue turns one stored string into a freshly allocated value of
// type t. An invalid reflect.Value with a nil error means "this kind is not
// supported, leave the field alone"; a non-nil error means the value was
// meant for this field and could not be parsed.
//
// The error never carries strValue. Option values are stored in the same
// table as the SMTP password, the OAuth client secret and the Turnstile secret
// key, and this error is written to the system log on every option-sync tick
// and returned to the admin API caller; strconv's and encoding/json's own
// messages quote the input, so they are replaced by parseKindError.
func decodeConfigValue(t reflect.Type, strValue string) (reflect.Value, error) {
	out := reflect.New(t).Elem()

	switch t.Kind() {
	case reflect.String:
		out.SetString(strValue)
	case reflect.Bool:
		boolValue, err := strconv.ParseBool(strValue)
		if err != nil {
			return reflect.Value{}, parseKindError(t)
		}
		out.SetBool(boolValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intValue, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			return reflect.Value{}, parseKindError(t)
		}
		out.SetInt(intValue)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintValue, err := strconv.ParseUint(strValue, 10, 64)
		if err != nil {
			return reflect.Value{}, parseKindError(t)
		}
		out.SetUint(uintValue)
	case reflect.Float32, reflect.Float64:
		floatValue, err := strconv.ParseFloat(strValue, 64)
		if err != nil {
			return reflect.Value{}, parseKindError(t)
		}
		out.SetFloat(floatValue)
	case reflect.Pointer:
		// "null" 表示清空指针字段
		if strValue == "null" {
			return reflect.Zero(t), nil
		}
		fresh := reflect.New(t.Elem())
		if err := json.Unmarshal([]byte(strValue), fresh.Interface()); err != nil {
			return reflect.Value{}, parseKindError(t)
		}
		return fresh, nil
	case reflect.Map, reflect.Slice, reflect.Struct:
		// 复杂类型使用JSON反序列化到全新对象（copy-on-write 的关键）
		if err := json.Unmarshal([]byte(strValue), out.Addr().Interface()); err != nil {
			return reflect.Value{}, parseKindError(t)
		}
	default:
		// 跳过不支持的类型
		return reflect.Value{}, nil
	}

	return out, nil
}

// parseKindError names what the stored string had to be, and nothing else.
func parseKindError(t reflect.Type) error {
	return errors.New("value is not a valid " + configValueKind(t))
}

// configValueKind is the operator-facing name of a config field's type.
func configValueKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "non-negative integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Map:
		return "JSON object"
	case reflect.Slice:
		return "JSON array"
	default:
		return "JSON value"
	}
}

// ConfigToMap 将配置对象转换为map（导出函数）
func ConfigToMap(config interface{}) (map[string]string, error) {
	return configToMap(config)
}

// UpdateConfigFromMap 从map更新配置对象（导出函数）。
//
// It applies every field it can parse and returns nil for the ones it cannot,
// which are left at their previous values. That lenient contract is the one
// this function had before copy-on-write, and it is pinned by
// TestUpdateConfigFromMap_InvalidNumericSkipsField and
// TestUpdateConfigFromMap_BoolParsingVariants
// (cov_boot-settings_config_test.go); callers that need to know a value was
// rejected — the admin option write path, repo/option.go's handleConfigUpdate
// — call UpdateConfigFromMapStrict instead.
//
// The log line names the field and the type it had to be, never the value
// (see decodeConfigValue).
func UpdateConfigFromMap(config interface{}, configMap map[string]string) error {
	if err := applyConfigMap(config, configMap); err != nil {
		common.SysError("config value rejected (previous value kept): " + err.Error())
	}
	return nil
}

// UpdateConfigFromMapStrict is UpdateConfigFromMap with the rejected fields
// reported to the caller: the returned error joins one entry per key whose
// stored value could not be parsed into its field. The fields that did parse
// are applied either way, and a rejected field keeps the value it had. Like
// every error out of this package it names the field and the expected type
// only, so a caller may put it in an HTTP response body.
func UpdateConfigFromMapStrict(config interface{}, configMap map[string]string) error {
	return applyConfigMap(config, configMap)
}

// ValidateConfigValue reports whether value could be applied to the field of
// config named by key, WITHOUT changing anything. The admin write path uses it
// to refuse a value before the options row is persisted, so a typo cannot
// leave the stored row and the running configuration permanently disagreeing.
// An unknown key is not an error here: the key dispatch that owns "is this
// field known" lives in repo/option.go, and a config module legitimately
// ignores keys that are not its fields.
func ValidateConfigValue(config interface{}, key, value string) error {
	val := reflect.ValueOf(config)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return nil
	}
	val = val.Elem()
	if val.Kind() != reflect.Struct {
		return nil
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		fieldType := typ.Field(i)
		if !fieldType.IsExported() {
			continue
		}
		name := fieldType.Tag.Get("json")
		if name == "" || name == "-" {
			name = fieldType.Name
		}
		if name != key {
			continue
		}

		field := val.Field(i)
		if selfUnmarshalingPointee(field) {
			probe := reflect.New(field.Type().Elem())
			if err := json.Unmarshal([]byte(value), probe.Interface()); err != nil {
				return parseKindError(field.Type())
			}
			return nil
		}
		if _, err := decodeConfigValue(field.Type(), value); err != nil {
			// Returned bare (no key prefix): the caller has the key, and the
			// error names the expected type only, never the value.
			return err
		}
		return nil
	}

	return nil
}

// ExportAllConfigs 导出所有已注册的配置为扁平结构
func (cm *ConfigManager) ExportAllConfigs() map[string]string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	result := make(map[string]string)

	for name, cfg := range cm.configs {
		configMap, err := ConfigToMap(cfg)
		if err != nil {
			continue
		}

		// 使用 "模块名.配置项" 的格式添加到结果中
		for key, value := range configMap {
			result[name+"."+key] = value
		}
	}

	return result
}
