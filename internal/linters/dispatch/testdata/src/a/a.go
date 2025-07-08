package a

import "github.com/macabot/hypp"

func dispatch(d hypp.Dispatchable, p hypp.Payload) {}

func action1(state any, payload any) hypp.Dispatchable {
	_ = payload.(string)
	return state
}

func action2(state any, _ any) hypp.Dispatchable {
	return state
}

func action3(state any, payload any) hypp.Dispatchable {
	return state
}

func action4(state any, payload any) hypp.Dispatchable { // want `dispatchable performs multiple type assertions on the payload`
	_ = payload.(string)
	_ = payload.(int)
	return state
}

func main() {
	dispatch(action1, "foo") // ok
	dispatch(action1, 123)   // want `payload type int does not match expected type string`
	dispatch(action1, nil)   // want `payload should not be nil when the dispatchable expects type string`

	dispatch(action2, nil)   // ok
	dispatch(action2, "foo") // want `payload should be nil when the dispatchable ignores it`

	dispatch(action3, nil)   // ok
	dispatch(action3, "foo") // want `payload should be nil when the dispatchable ignores it`

	dispatch(action4, "foo")
}
