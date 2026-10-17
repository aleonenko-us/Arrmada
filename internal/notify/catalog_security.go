package notify

import "strconv"

// Security alerts. EventTwoFactorFailures has no bus topic: the sign-in code step
// (internal/httpapi/twofactor.go) emits it with EmitOnce, keyed by account, hour and count,
// so a busy bus can't drop it and nothing repeats it.
//
// It names no account: an account's name is what it signs in with (often an email), which
// no alert or webhook may carry. The log line written with it has the account's id.
const EventTwoFactorFailures = "security.two_factor_failures"

func init() {
	Register(EventDef{
		Key: EventTwoFactorFailures, Group: GroupNeedsYou, DefaultOn: true,
		Label: "Wrong two-factor codes",
		Hint:  "Someone signed in with the right password for an account and then kept getting its two-factor code wrong: the password is likely known to someone else.",
		Format: func(d map[string]any) (Message, bool) {
			n := num(d, "count")
			if n <= 0 {
				return Message{}, false
			}
			return Message{
				Title: "Wrong two-factor codes",
				Body: "🔐 " + strconv.FormatInt(n, 10) + " wrong two-factor codes for one account in the last hour, each after the right password. " +
					"The password is likely known to someone else: the log names the account; have its owner change the password.",
				Link: "/settings/users",
			}, true
		},
	})
}
