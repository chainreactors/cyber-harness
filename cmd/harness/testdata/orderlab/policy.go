package main

// Access policy is maintained separately from HTTP and persistence code.
func allowOrder(u User, o Order) bool {
	return u.Active
}

func allowDownload(u User, e Export, o Order) bool {
	return u.Active
}
