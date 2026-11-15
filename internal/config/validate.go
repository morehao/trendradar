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

// Allowed message formats per provider.
var (
	dingTalkMsgTypes = []string{"markdown", "text"}
	larkMsgTypes     = []string{"post", "text", "interactive"}
	weComMsgTypes    = []string{"markdown", "text"}
)

// notifyStrategies is the set of supported fan-out policies.
var notifyStrategies = []string{"broadcast"}

// validate enforces the notify rules that struct tags cannot express: when the
// stage is enabled at least one section and one channel must be on; every
// enabled channel needs a resolved webhookURL (an unresolved ${VAR} is an error
// and its value is never echoed); msgtype must be in the provider's allowed set;
// strategy must be known; maxBodyChars must be either 0 (unlimited) or in
// [200, 20000]. Everything here is a no-op when notify is disabled, so an
// untouched default block never fails validation.
func (n NotifyConfig) validate() error {
	if !n.IsEnabled() {
		return nil
	}
	if !n.Sections.TrendingEnabled() && !n.Sections.ReleasesEnabled() {
		return fmt.Errorf("notify.sections: at least one of trending/releases must be true when notify.enabled")
	}
	if !n.Channels.DingTalk.Enabled && !n.Channels.Lark.Enabled && !n.Channels.WeCom.Enabled {
		return fmt.Errorf("notify.channels: at least one channel must be enabled when notify.enabled")
	}
	if err := n.validateStrategy(); err != nil {
		return err
	}
	if n.MaxBodyChars != 0 && (n.MaxBodyChars < 200 || n.MaxBodyChars > 20000) {
		return fmt.Errorf("notify.maxBodyChars: want 0 (unlimited) or 200-20000, got %d", n.MaxBodyChars)
	}
	if err := validateChannel("dingtalk", n.Channels.DingTalk, dingTalkMsgTypes); err != nil {
		return err
	}
	if err := validateChannel("lark", n.Channels.Lark, larkMsgTypes); err != nil {
		return err
	}
	return validateChannel("wecom", n.Channels.WeCom, weComMsgTypes)
}

func (n NotifyConfig) validateStrategy() error {
	for _, s := range notifyStrategies {
		if n.Strategy == s {
			return nil
		}
	}
	return fmt.Errorf("notify.strategy: want %s, got %q", strings.Join(notifyStrategies, "|"), n.Strategy)
}

// validateChannel checks one channel. webhookURL is checked for an unresolved
// ${VAR} BEFORE the emptiness check so the placeholder itself (not the resolved
// value) is what gets reported — and the secret value is never printed.
func validateChannel(name string, ch NotifyChannel, allowedMsgTypes []string) error {
	if !ch.Enabled {
		return nil
	}
	if strings.Contains(ch.WebhookURL, "${") {
		// Do not echo the raw value: it may be a partially-expanded secret.
		return fmt.Errorf("notify.channels.%s.webhookURL: unresolved ${VAR} placeholder (set the environment variable)", name)
	}
	if strings.TrimSpace(ch.WebhookURL) == "" {
		return fmt.Errorf("notify.channels.%s.webhookURL: required when %s is enabled", name, name)
	}
	if ch.Secret != "" && strings.Contains(ch.Secret, "${") {
		return fmt.Errorf("notify.channels.%s.secret: unresolved ${VAR} placeholder (set the environment variable)", name)
	}
	for _, t := range allowedMsgTypes {
		if ch.MsgType == t {
			return nil
		}
	}
	return fmt.Errorf("notify.channels.%s.msgtype: want %s, got %q", name, strings.Join(allowedMsgTypes, "|"), ch.MsgType)
}
