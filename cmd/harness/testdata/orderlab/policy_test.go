package main

import "testing"

func TestMemberReadsOwnTenantOrder(t *testing.T) {
	u := User{ID: "member", Tenant: "a", Role: "member", Active: true, CanRead: true}
	o := Order{ID: "own", Tenant: "a"}
	if !allowOrder(u, o) {
		t.Fatal("member must retain normal order access")
	}
	if !allowDownload(u, Export{ID: "e", Owner: u.ID, OrderID: o.ID, Status: "completed"}, o) {
		t.Fatal("member must retain own completed export access")
	}
}
