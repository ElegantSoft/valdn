package valdn

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

type (
	Rules       map[string][]string
	Errors      map[string]string
	fieldsExist map[string]bool
)

var (
	TagName      = "valdn"
	TagSeparator = "|"
)

type validation struct {
	rules       Rules
	errors      Errors
	fieldsExist fieldsExist
}

// createNewValidation copies rules and initialise new validation with it.
// rules are copied in case they will be manipulated later it doesn't affect the original rules.
func createNewValidation(rules Rules) *validation {
	v := validation{
		rules:       copyRules(rules),
		errors:      make(Errors),
		fieldsExist: make(fieldsExist),
	}
	return &v
}

// Validate validates single value by rules.
// If an error is found it will not check the rest of the rules and return the error.
// It panics if one of the rules is not registered.
func Validate(name string, val interface{}, rules []string) error {
	for _, r := range rules {
		if r == "" {
			continue
		}

		rName, rVal, rFunc, rExist := getRuleInfo(r)
		if !rExist {
			panic("unknown rule: " + rName)
		}

		err := rFunc(name, val, rVal)
		if err != nil {
			return err
		}
	}
	return nil
}

// ValidateStruct validates struct, and it's nested fields by rules and returns Errors.
// It panics if val is not a struct.
// If an error is found it will not check the rest of the field's rules and continue to the next field.
// If a parent has error it's nested fields will not be validated.
// It panics if one of the rules is not registered.
// It panics if one of the nested fields is a map and it's type is not map[string]interface{}.
// It panics if one of the nested fields is a slice and it's type is not []interface{}.
func ValidateStruct(val interface{}, rules Rules) Errors {
	if !IsStruct(val) {
		panic("val is not a struct")
	}
	t := reflect.TypeOf(val)
	v := createNewValidation(rules)
	v.addTagRules(val, t, "")

	v.validateStruct(val, "")
	v.validateNonExistRequiredFields()

	return v.errors
}

// ValidateMap validates map, and it's nested fields by rules and returns Errors.
// If an error is found it will not check the rest of the field's rules and continue to the next field.
// If a parent has error it's nested fields will not be validated.
// It panics if one of the rules is not registered.
// It panics if one of the nested fields is a map and it's type is not map[string]interface{}.
// It panics if one of the nested fields is a slice and it's type is not []interface{}.
func ValidateMap(val map[string]interface{}, rules Rules) Errors {
	t := reflect.TypeOf(val)
	v := createNewValidation(rules)
	v.addTagRules(val, t, "")

	v.validateMap(val, "")
	v.validateNonExistRequiredFields()

	return v.errors
}

// ValidateSlice validates slice, and it's nested fields by rules and returns Errors.
// If an error is found it will not check the rest of the field's rules and continue to the next field.
// If a parent has error it's nested fields will not be validated.
// It panics if one of the rules is not registered.
// It panics if one of the nested fields is a map and it's type is not map[string]interface{}.
// It panics if one of the nested fields is a slice and it's type is not []interface{}.
func ValidateSlice(val []interface{}, rules Rules) Errors {
	t := reflect.TypeOf(val)
	v := createNewValidation(rules)
	v.addTagRules(val, t, "")

	v.validateSlice(val, "")
	v.validateNonExistRequiredFields()

	return v.errors
}

// ValidateJSON transforms JSON string to a map and validates it by rules and returns Errors.
// If an error is found it will not check the rest of the field's rules and continue to the next field.
// If parent has error it's nested fields will not be validated.
// It panics if val is not JSON.
// It panics if one of the rules is not registered.
func ValidateJSON(val string, rules Rules) Errors {
	var jsonMap map[string]interface{}

	err := json.Unmarshal([]byte(val), &jsonMap)
	if err != nil {
		panic(err)
	}

	return ValidateMap(jsonMap, rules)
}

// ValidateRequest validates request by rules and returns Errors.
// It validates request of content type: multipart/form-data, application/json and application/x-www-form-urlencoded.
// It validates url parameters.
// It panics if body is not compatible with header content type.
// It panics if one of the rules is not registered.
// If an error is found it will not check the rest of the field's rules and continue to the next field.
func ValidateRequest(r *http.Request, rules Rules) Errors {
	m := parseRequest(r, rules)
	return ValidateMap(m, rules)
}

func (v *validation) registerField(name string) {
	v.fieldsExist[name] = true
}

func (v *validation) addError(name string, err error) {
	v.errors[name] = err.Error()
}

func (v *validation) getFieldRules(name string) []string {
	val, ok := v.rules[name]
	if ok {
		return val
	}
	return v.rules[getParentName(name)+".*"]
}

func (v *validation) getParentRules(name string) []string {
	val, ok := v.rules[name]
	if ok {
		return val
	}
	if name != "" {
		return v.rules[getParentName(name)+".*"]
	}
	return []string{}
}

// addTagRules gets rules from struct tag for every field and adds them to field rules if field has no rules.
func (v *validation) addTagRules(val interface{}, t reflect.Type, parName string) {
	parName = makeParentNameJoinable(parName)

	if m, ok := val.(map[string]interface{}); ok {
		for k, i := range m {
			switch {
			case IsStruct(i), IsMap(i), IsSlice(i):
				v.addTagRules(i, reflect.TypeOf(i), parName+k)
			}
		}
	}

	if s, ok := val.([]interface{}); ok {
		for k, i := range s {
			switch {
			case IsStruct(i), IsMap(i), IsSlice(i):
				v.addTagRules(i, reflect.TypeOf(i), parName+toString(k))
			}
		}
	}

	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			typ := f.Type
			name := parName + f.Name
			tRules := f.Tag.Get(TagName)

			// add tag rules only if field has no rules
			_, ok := v.rules[name]
			if !ok && tRules != "" {
				v.rules[name] = strings.Split(tRules, TagSeparator)
			}

			switch typ.Kind() {
			case reflect.Struct, reflect.Map, reflect.Slice:
				v.addTagRules(f, typ, name)
			}
		}
	}
}

func (v *validation) validateStruct(val interface{}, name string) {
	r := v.getParentRules(name)

	if err := Validate(name, val, r); err != nil {
		v.addError(name, err)
		return
	}

	typ := reflect.TypeOf(val)
	value := reflect.ValueOf(val)
	v.validateStructFields(typ, value, name)
}

func (v *validation) validateMap(val interface{}, name string) {
	if !reflect.DeepEqual(reflect.TypeOf(val), reflect.TypeOf(map[string]interface{}{})) {
		panic(fmt.Errorf("error validating %v: type %T is not supported", name, val))
	}

	r := v.getParentRules(name)
	if err := Validate(name, val, r); err != nil {
		v.addError(name, err)
		return
	}

	v.validateMapFields(convertInterfaceToMap(val), name)
}

func (v *validation) validateSlice(val interface{}, name string) {
	if !reflect.DeepEqual(reflect.TypeOf(val), reflect.TypeOf([]interface{}{})) {
		panic(fmt.Errorf("error validating %v: type %T is not supported", name, val))
	}

	r := v.getParentRules(name)
	if err := Validate(name, val, r); err != nil {
		v.addError(name, err)
		return
	}

	v.validateSliceFields(convertInterfaceToSlice(val), name)
}

func (v *validation) validateByType(name string, t reflect.Type, val interface{}) {
	v.registerField(name)
	rules := v.getFieldRules(name)

	switch t.Kind() {
	case reflect.Struct:
		v.validateStruct(val, name)
	case reflect.Map:
		v.validateMap(val, name)
	case reflect.Slice:
		v.validateSlice(val, name)
	default:
		err := Validate(name, val, rules)
		if err != nil {
			v.addError(name, err)
		}
	}
}

func (v *validation) validateStructFields(parTyp reflect.Type, parVal reflect.Value, parName string) {
	parName = makeParentNameJoinable(parName)
	for i := 0; i < parTyp.NumField(); i++ {
		name, typ, val := getStructFieldInfo(i, parTyp, parVal, parName)
		// ignore unexported field
		if !val.CanInterface() {
			continue
		}
		v.validateByType(name, typ, val.Interface())
	}
}

func (v *validation) validateMapFields(val map[string]interface{}, parName string) {
	parName = makeParentNameJoinable(parName)
	for name, value := range val {
		name = parName + name
		typ := reflect.TypeOf(value)
		v.validateByType(name, typ, value)
	}
}

func (v *validation) validateSliceFields(val []interface{}, parName string) {
	parName = makeParentNameJoinable(parName)
	for idx, value := range val {
		name := parName + toString(idx)
		typ := reflect.TypeOf(value)
		v.validateByType(name, typ, value)
	}
}

func (v *validation) validateNonExistRequiredFields() {
	for name, rules := range v.rules {
		if name == "*" {
			continue
		}
		for _, r := range rules {
			rName, rVal := splitRuleNameAndRuleValue(r)
			if rName == "required" {
				_, ok := v.fieldsExist[name]
				if !ok {
					v.addError(name, errors.New(GetErrMsg("required", rVal, name, "")))
				}
			}
		}
	}
}
