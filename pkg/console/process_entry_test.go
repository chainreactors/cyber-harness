package console
import ("os"; "testing"; "github.com/chainreactors/cyber/pkg/commands")
func TestMain(m *testing.M) { if code,handled := commands.RunShellCommandProxy(); handled { os.Exit(code) }; os.Exit(m.Run()) }
