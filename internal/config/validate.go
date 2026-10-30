package config

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/locales/zh_Hans_CN"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	zhtrans "github.com/go-playground/validator/v10/translations/zh"
)

// NOTE: this mirrors golib/gvalidate, but fixes a bug there: gvalidate calls
// ut.New(zh_Hans_CN.New()) with a fallback only, which never populates the
// translator map, so uni.GetTranslator always reports found=false and every
// Check() fails. Passing the locale as a *supported* locale (second arg) fixes
// it. Once golib fixes gvalidate we can switch back to gvalidate.Check.

// FieldError is one validation failure.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e FieldError) Error() string { return e.Message }

// Errors is a list of validation failures.
type Errors []FieldError

func (es Errors) Error() string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, "; ")
}

var (
	validatorOnce  sync.Once
	validatorInst  *validator.Validate
	validatorTrans ut.Translator
	validatorErr   error
)

func newValidator() (*validator.Validate, ut.Translator, error) {
	v := validator.New()
	loc := zh_Hans_CN.New()
	// Register the locale as a supported locale so GetTranslator finds it.
	uni := ut.New(loc, loc)
	trans, found := uni.GetTranslator(loc.Locale())
	if !found {
		return nil, nil, fmt.Errorf("validate: translator %q not found", loc.Locale())
	}
	if err := zhtrans.RegisterDefaultTranslations(v, trans); err != nil {
		return nil, nil, fmt.Errorf("validate: register translations: %w", err)
	}
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		if label := f.Tag.Get("label"); label != "" {
			return label
		}
		if tag := f.Tag.Get("yaml"); tag != "" && tag != "-" {
			return tag
		}
		return f.Name
	})
	return v, trans, nil
}

func validateStruct(data any) error {
	validatorOnce.Do(func() {
		validatorInst, validatorTrans, validatorErr = newValidator()
	})
	if validatorErr != nil {
		return validatorErr
	}
	err := validatorInst.Struct(data)
	if err == nil {
		return nil
	}
	var ve validator.ValidationErrors
	if !asValidationErrors(err, &ve) {
		return err
	}
	out := make(Errors, 0, len(ve))
	for _, fe := range ve {
		out = append(out, FieldError{Field: fe.Field(), Message: fe.Translate(validatorTrans)})
	}
	return out
}

// asValidationErrors avoids importing errors just for one call.
func asValidationErrors(err error, target *validator.ValidationErrors) bool {
	if ve, ok := err.(validator.ValidationErrors); ok {
		*target = ve
		return true
	}
	return false
}
