package service

func validPrivateRuntimeScope(environment, market string) bool {
	return (environment == "testnet" && market == "") ||
		(environment == "live" && market == "spot")
}
