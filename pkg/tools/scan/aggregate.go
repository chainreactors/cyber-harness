package scan

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
)

var firstURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

type assetBucket struct {
	asset     output.Asset
	keys      map[string]struct{}
	itemIndex map[string]int
}

type assetBuilder struct {
	buckets []*assetBucket
	byKey   map[string]*assetBucket
}

func AggregateStructuredResult(result *output.Result) []output.Asset {
	if result == nil {
		return nil
	}

	builder := newAssetBuilder()
	for _, service := range result.Services {
		builder.addService(service)
		if service != nil {
			for _, fw := range service.Frameworks {
				if fw == nil {
					continue
				}
				builder.addFrameworkFingerprint(service.GetTarget(), fw.Name, fw.IsFocus, capGogoPortscan)
			}
		}
	}
	for _, probe := range result.WebProbes {
		builder.addWebProbe(probe)
		if probe != nil {
			for _, fw := range probe.Frameworks {
				if fw == nil {
					continue
				}
				builder.addFrameworkFingerprint(probe.UrlString, fw.Name, fw.IsFocus, probe.Source.Name())
			}
		}
	}
	for i := range result.Loots {
		builder.addLoot(&result.Loots[i])
	}
	for _, err := range result.Errors {
		builder.addError(err)
	}
	return builder.assets()
}

func newAssetBuilder() *assetBuilder {
	return &assetBuilder{byKey: make(map[string]*assetBucket)}
}

func (b *assetBuilder) addService(service *sdktypes.GOGOResult) {
	if service == nil {
		return
	}
	target := gogoServiceAssetTarget(service)
	hostPort := ""
	if service.Ip != "" && service.Port != "" {
		hostPort = service.Ip + ":" + service.Port
	}
	serviceTarget := service.GetTarget()
	keys := targetKeys(target, serviceTarget, hostPort)
	svcName := output.FirstNonEmpty(service.Protocol, service.Midware)
	data := assetData(
		"ip", service.Ip,
		"port", service.Port,
		"protocol", service.Protocol,
		"service", svcName,
		"banner", service.Midware,
		"is_web", service.IsHttp(),
	)
	item := output.AssetItem{
		Kind:    output.AssetItemService,
		Source:  capGogoPortscan,
		Target:  serviceTarget,
		Title:   output.FirstNonEmpty(svcName, service.Protocol, service.Midware),
		Summary: service.Midware,
		Tags:    output.CompactStrings(service.Protocol, svcName, service.Port),
		Data:    data,
	}
	b.addItem(target, keys, "service|"+strings.Join(sortedStrings(keys), "|"), item)
}

func (b *assetBuilder) addWebProbe(probe *sdktypes.SprayResult) {
	if probe == nil || probe.UrlString == "" {
		return
	}
	if !strings.Contains(probe.UrlString, "://") {
		return
	}
	target := webAssetTarget(probe.UrlString)
	sourceName := probe.Source.Name()
	keys := targetKeys(target, probe.UrlString)
	status := ""
	if probe.Status > 0 {
		status = strconv.Itoa(probe.Status)
	}
	path := output.WebPath(probe.UrlString)
	fingerNames := parsers.FrameworkNames(probe.Frameworks)
	data := assetData(
		"url", probe.UrlString,
		"path", path,
		"status", probe.Status,
		"length", probe.BodyLength,
		"title", probe.Title,
		"fingers", fingerNames,
		"validated", isSprayValidated(sourceName),
	)
	tags := append([]string{sourceName}, fingerNames...)
	if isSprayValidated(sourceName) {
		tags = append(tags, "validated")
	}
	item := output.AssetItem{
		Kind:    output.AssetItemPath,
		Source:  sourceName,
		Target:  probe.UrlString,
		Status:  status,
		Title:   probe.Title,
		Summary: path,
		Tags:    output.CompactStrings(tags...),
		Data:    data,
	}
	identity := "path|" + canonicalKey(probe.UrlString) + "|host=" + strings.ToLower(probe.Host)
	b.addItem(target, keys, identity, item)
}

// isSprayValidated returns true when the source capability is a spray
// pipeline stage. Spray results that reach the collector have already
// survived spray's baseline comparison (body-length + simhash fuzzy
// deduplication), so they represent pages that are structurally distinct
// from the site's default response — higher signal for the -F report.
func isSprayValidated(source string) bool {
	switch source {
	case capSprayCheck, capSprayCrawl, capSprayPlugins, capSprayBrute:
		return true
	default:
		return false
	}
}

func (b *assetBuilder) addFrameworkFingerprint(targetStr, name string, focus bool, source string) {
	if name == "" {
		return
	}
	target := assetTargetFromValues(targetStr)
	keys := targetKeys(target, targetStr)
	data := assetData(
		"name", name,
		"focus", focus,
	)
	item := output.AssetItem{
		Kind:   output.AssetItemFingerprint,
		Source: source,
		Target: targetStr,
		Title:  name,
		Tags:   output.CompactStrings(source, name),
		Data:   data,
	}
	identity := "fingerprint|" + canonicalKey(targetStr) + "|" + strings.ToLower(name)
	b.addItem(target, keys, identity, item)
}

func (b *assetBuilder) addLoot(loot *output.Loot) {
	if loot == nil {
		return
	}
	target := assetTargetFromValues(loot.Target)
	keys := targetKeys(target, loot.Target)
	status := output.FirstNonEmpty(loot.Priority, output.AssetItemLoot)
	data := make(map[string]any)
	data["kind"] = loot.Kind
	for k, v := range loot.Data {
		data[k] = v
	}
	item := output.AssetItem{
		Kind:    output.AssetItemLoot,
		Source:  loot.Kind,
		Target:  loot.Target,
		Status:  status,
		Title:   loot.Description,
		Summary: loot.Description,
		Tags:    output.CompactStrings(append([]string{loot.Kind}, loot.Tags...)...),
		Data:    data,
	}
	identity := strings.Join(output.CompactStrings(
		output.AssetItemLoot,
		loot.Kind,
		loot.Target,
		loot.Description,
	), "|")
	b.addItem(target, keys, identity, item)
}

func (b *assetBuilder) addError(err output.Error) {
	keys := targetKeys("scan")
	item := output.AssetItem{
		Kind:    output.AssetItemError,
		Source:  err.Source,
		Target:  "scan",
		Status:  output.AssetItemError,
		Summary: err.Message,
		Data:    assetData("message", err.Message),
	}
	identity := "error|" + err.Source + "|" + err.Message
	b.addItem("Scan", keys, identity, item)
}

func (b *assetBuilder) addItem(target string, keys []string, identity string, item output.AssetItem) {
	target = output.FirstNonEmpty(target, item.Target, "Scan")
	if len(keys) == 0 {
		keys = targetKeys(target)
	}
	bucket := b.findBucket(keys)
	if bucket == nil {
		bucket = &assetBucket{
			asset: output.Asset{
				Target: target,
			},
			keys:      make(map[string]struct{}),
			itemIndex: make(map[string]int),
		}
		b.buckets = append(b.buckets, bucket)
	}
	bucket.asset.Target = preferredAssetTarget(bucket.asset.Target, target)
	for _, key := range keys {
		if key == "" {
			continue
		}
		bucket.keys[key] = struct{}{}
		b.byKey[key] = bucket
	}
	if identity == "" {
		identity = itemIdentity(item)
	}
	if existing, ok := bucket.itemIndex[identity]; ok {
		bucket.asset.Items[existing] = mergeAssetItem(bucket.asset.Items[existing], item)
		return
	}
	bucket.itemIndex[identity] = len(bucket.asset.Items)
	bucket.asset.Items = append(bucket.asset.Items, normalizeAssetItem(item))
}

func (b *assetBuilder) findBucket(keys []string) *assetBucket {
	for _, key := range sortedStrings(keys) {
		if bucket := b.byKey[key]; bucket != nil {
			return bucket
		}
	}
	return nil
}

func (b *assetBuilder) assets() []output.Asset {
	out := make([]output.Asset, 0, len(b.buckets))
	for _, bucket := range b.buckets {
		asset := bucket.asset
		sortAssetItems(asset.Items)
		asset.Target = output.FirstNonEmpty(asset.Target, "Scan")
		asset.Key = preferredAssetKey(bucket.keys, asset.Target)
		asset.ID = "asset:" + asset.Key
		asset.Title = deriveAssetTitle(asset)
		asset.Status = deriveAssetStatus(asset.Items)
		out = append(out, asset)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func gogoServiceAssetTarget(service *sdktypes.GOGOResult) string {
	if service.IsHttp() {
		scheme := strings.ToLower(strings.TrimSpace(service.Protocol))
		if !strings.HasPrefix(scheme, "http") {
			if service.Port == "443" {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		if service.Ip != "" && service.Port != "" {
			return scheme + "://" + service.Ip + ":" + service.Port
		}
	}
	return assetTargetFromValues(service.GetTarget())
}

func webAssetTarget(rawURL string) string {
	if origin := urlOrigin(rawURL); origin != "" {
		return origin
	}
	return rawURL
}

func assetTargetFromValues(values ...string) string {
	for _, value := range values {
		if origin := urlOrigin(value); origin != "" {
			return origin
		}
		if first := firstURL(value); first != "" {
			if origin := urlOrigin(first); origin != "" {
				return origin
			}
			return first
		}
	}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "Scan"
}

func targetKeys(values ...string) []string {
	seen := make(map[string]struct{})
	for _, value := range values {
		addTargetKeys(seen, value)
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func addTargetKeys(keys map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	addCanonicalKey(keys, value)
	withoutHost := strings.Split(value, "|host=")[0]
	addCanonicalKey(keys, withoutHost)
	if first := firstURL(withoutHost); first != "" {
		if canonicalKey(first) != canonicalKey(withoutHost) {
			addTargetKeys(keys, first)
		}
	}
	if origin := urlOrigin(withoutHost); origin != "" {
		addCanonicalKey(keys, origin)
	}
	if host := urlHost(withoutHost); host != "" {
		addCanonicalKey(keys, host)
	}
	if normalized := normalizedURL(withoutHost); normalized != "" {
		addCanonicalKey(keys, normalized)
	}
}

func addCanonicalKey(keys map[string]struct{}, value string) {
	if key := canonicalKey(value); key != "" {
		keys[key] = struct{}{}
	}
}

func canonicalKey(value string) string {
	value = strings.Trim(value, " \t\r\n\"'<>[](),")
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

func normalizedURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" || path == "/" {
		path = ""
	}
	query := ""
	if parsed.RawQuery != "" {
		query = "?" + parsed.RawQuery
	}
	return strings.ToLower(parsed.Scheme + "://" + stripDefaultPort(parsed) + path + query)
}

func urlOrigin(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + stripDefaultPort(parsed))
}

func urlHost(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(stripDefaultPort(parsed))
}

func stripDefaultPort(u *url.URL) string {
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		return host
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		return host
	}
	return host + ":" + port
}

func firstURL(value string) string {
	if value == "" {
		return ""
	}
	match := firstURLPattern.FindString(value)
	return strings.Trim(match, " \t\r\n\"'<>[](),")
}

func preferredAssetTarget(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" || strings.EqualFold(current, "scan") {
		return next
	}
	if next == "" {
		return current
	}
	if urlOrigin(next) != "" && urlOrigin(current) == "" {
		return next
	}
	return current
}

func preferredAssetKey(keys map[string]struct{}, target string) string {
	targetKey := canonicalKey(target)
	if targetKey != "" {
		if _, ok := keys[targetKey]; ok {
			return targetKey
		}
	}
	sorted := make([]string, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	if len(sorted) > 0 {
		return sorted[0]
	}
	return canonicalKey(output.FirstNonEmpty(target, "scan"))
}

func deriveAssetTitle(asset output.Asset) string {
	if title := firstItemText(asset.Items, func(item output.AssetItem) bool {
		return (item.Kind == output.AssetItemLoot || item.Kind == output.AssetItemNote) && item.Status == "confirmed"
	}); title != "" {
		return title
	}
	if title := firstItemText(asset.Items, func(item output.AssetItem) bool {
		return item.Kind == output.AssetItemNote && item.Status == "info"
	}); title != "" {
		return title
	}
	if title := firstItemText(asset.Items, func(item output.AssetItem) bool {
		return item.Kind == output.AssetItemLoot || item.Kind == output.AssetItemNote
	}); title != "" {
		return title
	}
	if title := firstItemText(asset.Items, func(item output.AssetItem) bool {
		return item.Kind == output.AssetItemPath && item.Title != ""
	}); title != "" {
		return title
	}
	for _, item := range asset.Items {
		if item.Kind != output.AssetItemService || item.Data == nil {
			continue
		}
		if banner, ok := item.Data["banner"].(string); ok && strings.TrimSpace(banner) != "" {
			return strings.TrimSpace(banner)
		}
	}
	return asset.Target
}

func firstItemText(items []output.AssetItem, match func(output.AssetItem) bool) string {
	for _, item := range items {
		if !match(item) {
			continue
		}
		if text := output.FirstNonEmpty(item.Title, item.Summary); text != "" {
			return text
		}
	}
	return ""
}

func deriveAssetStatus(items []output.AssetItem) string {
	bestStatus := ""
	bestRank := 0
	for _, item := range items {
		status := item.Status
		if item.Kind == output.AssetItemLoot && status == "" {
			status = output.AssetItemLoot
		}
		if item.Kind == output.AssetItemError && status == "" {
			status = output.AssetItemError
		}
		rank := assetStatusRank(item.Kind, status)
		if rank > bestRank {
			bestRank = rank
			bestStatus = status
		}
	}
	return bestStatus
}

func assetStatusRank(kind, status string) int {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "confirmed":
		return 100
	case string(priorityCritical):
		return 95
	case string(priorityHigh):
		return 90
	case output.AssetItemLoot:
		return 85
	case string(priorityMedium):
		return 70
	case "info":
		return 60
	case string(priorityLow):
		return 50
	case "inconclusive":
		return 40
	case "not_confirmed":
		return 30
	case "failed", output.AssetItemError:
		return 20
	}
	if kind == output.AssetItemLoot {
		return 85
	}
	if kind == output.AssetItemError {
		return 20
	}
	if kind == output.AssetItemResponse {
		return 10
	}
	return 0
}

func sortAssetItems(items []output.AssetItem) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := assetItemRank(items[i].Kind), assetItemRank(items[j].Kind)
		if ri != rj {
			return ri < rj
		}
		vi, vj := output.HasTag(items[i].Tags, "validated"), output.HasTag(items[j].Tags, "validated")
		if vi != vj {
			return vi
		}
		left := fmt.Sprintf("%s|%s|%s", items[i].Target, items[i].Title, items[i].Summary)
		right := fmt.Sprintf("%s|%s|%s", items[j].Target, items[j].Title, items[j].Summary)
		return left < right
	})
}

func assetItemRank(kind string) int {
	switch kind {
	case output.AssetItemService:
		return 10
	case output.AssetItemFingerprint:
		return 20
	case output.AssetItemLoot:
		return 30
	case output.AssetItemNote:
		return 40
	case output.AssetItemResponse:
		return 45
	case output.AssetItemPath:
		return 50
	case output.AssetItemError:
		return 60
	default:
		return 90
	}
}

func mergeAssetItem(current, next output.AssetItem) output.AssetItem {
	current.Kind = output.FirstNonEmpty(current.Kind, next.Kind)
	current.Source = output.FirstNonEmpty(current.Source, next.Source)
	current.Target = output.FirstNonEmpty(current.Target, next.Target)
	current.Status = output.FirstNonEmpty(current.Status, next.Status)
	current.Title = output.FirstNonEmpty(current.Title, next.Title)
	current.Summary = output.FirstNonEmpty(current.Summary, next.Summary)
	current.Detail = output.FirstNonEmpty(current.Detail, next.Detail)
	current.Raw = output.FirstNonEmpty(current.Raw, next.Raw)
	current.Tags = output.CompactStrings(append(current.Tags, next.Tags...)...)
	if current.Data == nil {
		current.Data = next.Data
	} else {
		for key, value := range next.Data {
			if isEmptyAssetData(value) {
				continue
			}
			if isEmptyAssetData(current.Data[key]) {
				current.Data[key] = value
			}
		}
	}
	return normalizeAssetItem(current)
}

func normalizeAssetItem(item output.AssetItem) output.AssetItem {
	item.Kind = strings.TrimSpace(item.Kind)
	item.Source = strings.TrimSpace(item.Source)
	item.Target = strings.TrimSpace(item.Target)
	item.Status = strings.TrimSpace(item.Status)
	item.Title = strings.TrimSpace(item.Title)
	item.Summary = strings.TrimSpace(item.Summary)
	item.Detail = strings.TrimSpace(item.Detail)
	item.Raw = strings.TrimSpace(item.Raw)
	item.Tags = output.CompactStrings(item.Tags...)
	if len(item.Data) == 0 {
		item.Data = nil
	}
	return item
}

func itemIdentity(item output.AssetItem) string {
	return strings.Join(output.CompactStrings(item.Kind, item.Source, item.Target, item.Status, item.Title, item.Summary, item.Raw), "|")
}

func assetData(values ...any) map[string]any {
	data := make(map[string]any)
	for i := 0; i+1 < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok || key == "" || isEmptyAssetData(values[i+1]) {
			continue
		}
		data[key] = values[i+1]
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

func isEmptyAssetData(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case int:
		return v == 0
	case bool:
		return !v
	case []string:
		return len(output.CompactStrings(v...)) == 0
	default:
		return false
	}
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
