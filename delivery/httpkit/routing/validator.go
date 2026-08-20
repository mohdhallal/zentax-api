package routing

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

type fieldError struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}

type fieldErrors []fieldError

func (fe fieldErrors) Error() string {
	parts := make([]string, 0, len(fe))
	for _, e := range fe {
		parts = append(parts, e.Field+": "+e.Issue)
	}
	return strings.Join(parts, "; ")
}

var validate *validator.Validate

func init() {
	validate = validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return field.Name
		}
		return name
	})
}

func newInstance(schema any) (any, reflect.Type) {
	schemaType := reflect.TypeOf(schema)
	if schemaType.Kind() == reflect.Pointer {
		schemaType = schemaType.Elem()
	}
	return reflect.New(schemaType).Interface(), schemaType
}

func validateBody(raw, schema any) (any, error) {
	target, _ := newInstance(schema)

	if raw != nil {
		bytes, err := json.Marshal(raw)
		if err != nil {
			return nil, errors.New("body must be a JSON object")
		}
		dec := json.NewDecoder(strings.NewReader(string(bytes)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(target); err != nil {
			if unknown, ok := extractUnknownField(err); ok {
				return nil, fieldErrors{{Field: unknown, Issue: "unknown field"}}
			}
			return nil, errors.New("body must be a JSON object")
		}
	}

	applyDefaults(target)

	if err := validate.Struct(target); err != nil {
		return nil, formatValidationErrors(err)
	}

	return target, nil
}

func validateQuery(raw map[string][]string, schema any) (any, error) {
	target, structType := newInstance(schema)

	if err := decodeMultiValueMap(raw, target, structType); err != nil {
		return nil, err
	}

	applyDefaults(target)

	if err := validate.Struct(target); err != nil {
		return nil, formatValidationErrors(err)
	}

	return target, nil
}

func validateParams(raw map[string]string, schema any) (any, error) {
	target, structType := newInstance(schema)

	if err := decodeStringMap(raw, target, structType); err != nil {
		return nil, err
	}

	applyDefaults(target)

	if err := validate.Struct(target); err != nil {
		return nil, formatValidationErrors(err)
	}

	return target, nil
}

func decodeMultiValueMap(raw map[string][]string, target any, structType reflect.Type) error {
	v := reflect.ValueOf(target).Elem()
	return decodeMultiValueMapValue(raw, v, structType)
}

func decodeMultiValueMapValue(raw map[string][]string, v reflect.Value, structType reflect.Type) error {
	var errs fieldErrors

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		fieldVal := v.Field(i)

		if field.Anonymous && fieldVal.Kind() == reflect.Struct {
			if err := decodeMultiValueMapValue(raw, fieldVal, field.Type); err != nil {
				var fe fieldErrors
				if errors.As(err, &fe) {
					errs = append(errs, fe...)
				} else {
					return err
				}
			}
			continue
		}

		jsonTag := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		values, present := raw[jsonTag]
		if !present || len(values) == 0 {
			continue
		}

		if fieldVal.Kind() == reflect.Slice && fieldVal.Type().Elem().Kind() == reflect.String {
			slice := reflect.MakeSlice(fieldVal.Type(), len(values), len(values))
			for j, val := range values {
				slice.Index(j).SetString(strings.TrimSpace(val))
			}
			fieldVal.Set(slice)
			continue
		}

		if err := setFieldFromString(fieldVal, values[0]); err != nil {
			errs = append(errs, fieldError{Field: jsonTag, Issue: err.Error()})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func decodeStringMap(raw map[string]string, target any, structType reflect.Type) error {
	v := reflect.ValueOf(target).Elem()
	return decodeStringMapValue(raw, v, structType)
}

func decodeStringMapValue(raw map[string]string, v reflect.Value, structType reflect.Type) error {
	var errs fieldErrors

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		fieldVal := v.Field(i)

		if field.Anonymous && fieldVal.Kind() == reflect.Struct {
			if err := decodeStringMapValue(raw, fieldVal, field.Type); err != nil {
				var fe fieldErrors
				if errors.As(err, &fe) {
					errs = append(errs, fe...)
				} else {
					return err
				}
			}
			continue
		}

		jsonTag := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		rawVal, present := raw[jsonTag]
		if !present || rawVal == "" {
			continue
		}

		if err := setFieldFromString(fieldVal, rawVal); err != nil {
			errs = append(errs, fieldError{Field: jsonTag, Issue: err.Error()})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func setFieldFromString(fieldVal reflect.Value, raw string) error {
	t := fieldVal.Type()

	if t.Kind() == reflect.Ptr {
		elem := reflect.New(t.Elem())
		if err := setFieldFromString(elem.Elem(), raw); err != nil {
			return err
		}
		fieldVal.Set(elem)
		return nil
	}

	switch t.Kind() {
	case reflect.String:
		fieldVal.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return errors.New("must be an integer")
		}
		fieldVal.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return errors.New("must be a positive integer")
		}
		fieldVal.SetUint(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return errors.New("must be a boolean")
		}
		fieldVal.SetBool(b)
	default:
		return errors.New("unsupported type " + t.Kind().String())
	}
	return nil
}

func applyDefaults(target any) {
	v := reflect.ValueOf(target)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	applyDefaultsValue(v)
}

func applyDefaultsValue(v reflect.Value) {
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldVal := v.Field(i)

		if field.Anonymous && fieldVal.Kind() == reflect.Struct {
			applyDefaultsValue(fieldVal)
			continue
		}

		defaultStr, ok := field.Tag.Lookup("default")
		if !ok {
			continue
		}

		if !fieldVal.IsZero() {
			continue
		}

		if fieldVal.Kind() == reflect.Ptr {
			elem := reflect.New(fieldVal.Type().Elem())
			_ = setFieldFromString(elem.Elem(), defaultStr)
			fieldVal.Set(elem)
			continue
		}

		_ = setFieldFromString(fieldVal, defaultStr)
	}
}

func extractUnknownField(err error) (string, bool) {
	msg := err.Error()
	prefix := "json: unknown field "
	if strings.HasPrefix(msg, prefix) {
		return strings.Trim(msg[len(prefix):], "\""), true
	}
	return "", false
}

func formatValidationErrors(err error) fieldErrors {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return fieldErrors{{Field: "_", Issue: err.Error()}}
	}

	errs := make(fieldErrors, 0, len(ve))
	for _, fe := range ve {
		errs = append(errs, fieldError{Field: fe.Field(), Issue: validationMessage(fe)})
	}
	return errs
}

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "required_without_all":
		return "is required when other fields are absent"
	case "email":
		return "invalid email format"
	case "uuid":
		return "must be a valid UUID"
	case "max":
		if fe.Kind() == reflect.String || (fe.Kind() == reflect.Ptr && fe.Type().Elem().Kind() == reflect.String) {
			return "must be at most " + fe.Param() + " characters"
		}
		return "must be at most " + fe.Param()
	case "min":
		if fe.Kind() == reflect.String || (fe.Kind() == reflect.Ptr && fe.Type().Elem().Kind() == reflect.String) {
			return "must be at least " + fe.Param() + " characters"
		}
		return "must be at least " + fe.Param()
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(fe.Param(), " ", ", ")
	default:
		return "failed validation: " + fe.Tag()
	}
}
