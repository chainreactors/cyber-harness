package service
import ("os"; "testing"; "github.com/chainreactors/aiscan/pkg/commands")
func TestMain(m *testing.M) { if code,handled := commands.RunShellCommandProxy(); handled { os.Exit(code) }; os.Exit(m.Run()) }
