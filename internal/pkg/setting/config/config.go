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
// publish freshly decoded values (see applyConfigMap); readers that must be
// race-free take it for reading through RLock/RUnlock.
//
// Lock order where both are held: cm.mutex first, then fieldMu (LoadFromDB,
// SaveToDB and ExportAllConfigs are the three places that hold both).
var fieldMu sync.RWMutex

// RLock acquires the configuration read lock. Accessors in the setting
// subpackages that a relay goroutine calls use it so their reads are ordered
// against the option-sync tick's writes; it is exported because those
// accessors live in other packages (model_setting, system_setting).
// Every RLock must be paired with an RUnlock, and nothing that holds it may
// call back into a function that writes configuration.
func RLock() { fieldMu.RLock() }

// RUnlock releases the configuration read lock taken by RLock.
func RUnlock() { fieldMu.RUnlock() }

// Lock acquires the configuration write lock. It is for a setting package
// publishing a replacement value into its own registered struct — the
// copy-on-write rule still holds, so the caller assigns a freshly built
// map/slice rather than writing into the one already published.
// model_setting.GetClaudeSettings's missing-"default" repair is the one caller
// today (`grep -rn "config.Lock()" --include=*.go .`).
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

	type pendingField struct {
		index int
		value reflect.Value
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
			val.Field(p.index).Set(p.value)
		}
		fieldMu.Unlock()
	}

	return errors.Join(errs...)
}

// decodeConfigValue turns one stored string into a freshly allocated value of
// type t. An invalid reflect.Value with a nil error means "this kind is not
// supported, leave the field alone"; a non-nil error means the value was
// meant for this field and could not be parsed.
func decodeConfigValue(t reflect.Type, strValue string) (reflect.Value, error) {
	out := reflect.New(t).Elem()

	switch t.Kind() {
	case reflect.String:
		out.SetString(strValue)
	case reflect.Bool:
		boolValue, err := strconv.ParseBool(strValue)
		if err != nil {
			return reflect.Value{}, err
		}
		out.SetBool(boolValue)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intValue, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			return reflect.Value{}, err
		}
		out.SetInt(intValue)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintValue, err := strconv.ParseUint(strValue, 10, 64)
		if err != nil {
			return reflect.Value{}, err
		}
		out.SetUint(uintValue)
	case reflect.Float32, reflect.Float64:
		floatValue, err := strconv.ParseFloat(strValue, 64)
		if err != nil {
			return reflect.Value{}, err
		}
		out.SetFloat(floatValue)
	case reflect.Pointer:
		// "null" 表示清空指针字段
		if strValue == "null" {
			return reflect.Zero(t), nil
		}
		fresh := reflect.New(t.Elem())
		if err := json.Unmarshal([]byte(strValue), fresh.Interface()); err != nil {
			return reflect.Value{}, err
		}
		return fresh, nil
	case reflect.Map, reflect.Slice, reflect.Struct:
		// 复杂类型使用JSON反序列化到全新对象（copy-on-write 的关键）
		if err := json.Unmarshal([]byte(strValue), out.Addr().Interface()); err != nil {
			return reflect.Value{}, err
		}
	default:
		// 跳过不支持的类型
		return reflect.Value{}, nil
	}

	return out, nil
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
func UpdateConfigFromMap(config interface{}, configMap map[string]string) error {
	if err := applyConfigMap(config, configMap); err != nil {
		common.SysError("config value rejected (previous value kept): " + err.Error())
	}
	return nil
}

// UpdateConfigFromMapStrict is UpdateConfigFromMap with the rejected fields
// reported to the caller: the returned error joins one entry per key whose
// stored value could not be parsed into its field. The fields that did parse
// are applied either way, and a rejected field keeps the value it had.
func UpdateConfigFromMapStrict(config interface{}, configMap map[string]string) error {
	return applyConfigMap(config, configMap)
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
