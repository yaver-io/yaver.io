using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Windows.Data.Json;
using Windows.Storage;
using Windows.Storage.Streams;

namespace YaverXbox.Services
{
    public sealed class DeviceCodeStart
    {
        public string UserCode { get; set; }
        public string DeviceCode { get; set; }
    }

    public sealed class MachineRow
    {
        public string DeviceId { get; set; }
        public string Name { get; set; }
        public string Detail { get; set; }
        public string State { get; set; }
    }

    public sealed class RelayRoute
    {
        public string HttpUrl { get; set; }
        public string Password { get; set; }
    }

    public sealed class TaskRow
    {
        public string Id { get; set; }
        public string Title { get; set; }
        public string Status { get; set; }
        public string Verification { get; set; }
        public string VideoClipId { get; set; }
        public string DisplayLine => string.Join(" · ", new[] { Status, Verification }.WhereNotEmpty());
    }

    public sealed class YaverApiClient
    {
        private const string ApiOrigin = "https://perceptive-minnow-557.eu-west-1.convex.site";
        private readonly HttpClient http = new HttpClient { Timeout = TimeSpan.FromSeconds(30) };

        public async Task<DeviceCodeStart> StartDeviceCodeAsync()
        {
            var settings = ApplicationData.Current.LocalSettings;
            var installationId = settings.Values["installationId"] as string;
            if (string.IsNullOrWhiteSpace(installationId))
            {
                installationId = Guid.NewGuid().ToString("N");
                settings.Values["installationId"] = installationId;
            }
            var body = new JsonObject
            {
                ["machineName"] = JsonValue.CreateStringValue("Xbox"),
                ["platform"] = JsonValue.CreateStringValue("xbox"),
                ["environment"] = JsonValue.CreateStringValue("tv"),
                ["deviceId"] = JsonValue.CreateStringValue(installationId)
            };
            var response = await http.PostAsync(ApiOrigin + "/auth/device-code", Json(body));
            await EnsureSuccess(response, "Could not start sign-in");
            var json = JsonObject.Parse(await response.Content.ReadAsStringAsync());
            return new DeviceCodeStart { UserCode = Text(json, "userCode"), DeviceCode = Text(json, "deviceCode") };
        }

        public async Task<string> WaitForTokenAsync(string deviceCode, CancellationToken cancellation)
        {
            while (true)
            {
                cancellation.ThrowIfCancellationRequested();
                var response = await http.GetAsync(ApiOrigin + "/auth/device-code/poll?device_code=" + Uri.EscapeDataString(deviceCode));
                await EnsureSuccess(response, "Sign-in polling failed");
                var json = JsonObject.Parse(await response.Content.ReadAsStringAsync());
                var status = Text(json, "status");
                if (status == "expired") throw new InvalidOperationException("The code expired. Start again.");
                if (status == "authorized")
                {
                    var token = Text(json, "token");
                    if (!string.IsNullOrWhiteSpace(token)) return token;
                    var claim = new JsonObject { ["deviceCode"] = JsonValue.CreateStringValue(deviceCode) };
                    var handle = Text(json, "claimHandle");
                    if (!string.IsNullOrWhiteSpace(handle)) claim["claimHandle"] = JsonValue.CreateStringValue(handle);
                    var claimed = await http.PostAsync(ApiOrigin + "/auth/device-code/claim", Json(claim));
                    await EnsureSuccess(claimed, "Could not claim the approved session");
                    var claimJson = JsonObject.Parse(await claimed.Content.ReadAsStringAsync());
                    token = Text(claimJson, "token");
                    if (!string.IsNullOrWhiteSpace(token)) return token;
                }
                await Task.Delay(TimeSpan.FromSeconds(2), cancellation);
            }
        }

        public async Task<IReadOnlyList<MachineRow>> GetMachinesAsync(string token)
        {
            var request = new HttpRequestMessage(HttpMethod.Get, ApiOrigin + "/devices/list");
            request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", token);
            request.Headers.Add("X-Yaver-Surface", "xbox");
            var response = await http.SendAsync(request);
            if (response.StatusCode == HttpStatusCode.Unauthorized) throw new UnauthorizedAccessException();
            await EnsureSuccess(response, "Machine discovery failed");
            var json = JsonObject.Parse(await response.Content.ReadAsStringAsync());
            var rows = new List<MachineRow>();
            if (!json.ContainsKey("devices") || json["devices"].ValueType != JsonValueType.Array) return rows;
            foreach (var value in json["devices"].GetArray())
            {
                if (value.ValueType != JsonValueType.Object) continue;
                var item = value.GetObject();
                var name = Text(item, "name");
                if (string.IsNullOrWhiteSpace(name)) name = Text(item, "alias");
                if (string.IsNullOrWhiteSpace(name)) name = Text(item, "deviceId");
                var platform = Text(item, "platform");
                var version = Text(item, "agentVersion");
                var online = item.ContainsKey("isOnline") && item["isOnline"].ValueType == JsonValueType.Boolean && item["isOnline"].GetBoolean();
                rows.Add(new MachineRow
                {
                    DeviceId = Text(item, "deviceId"),
                    Name = name,
                    Detail = string.Join(" · ", new[] { platform, string.IsNullOrWhiteSpace(version) ? null : "agent " + version }.WhereNotEmpty()),
                    State = online ? "● online" : "○ offline"
                });
            }
            return rows;
        }

        public async Task<RelayRoute> GetRelayAsync(string token)
        {
            var response = await SendPlatformAsync(HttpMethod.Get, "/config", token);
            await EnsureSuccess(response, "Relay discovery failed");
            var json = JsonObject.Parse(await response.Content.ReadAsStringAsync());
            if (!json.ContainsKey("relayServers") || json["relayServers"].ValueType != JsonValueType.Array)
                throw new InvalidOperationException("No relay is configured for this account.");
            foreach (var value in json["relayServers"].GetArray())
            {
                if (value.ValueType != JsonValueType.Object) continue;
                var row = value.GetObject();
                var url = Text(row, "httpUrl");
                if (!string.IsNullOrWhiteSpace(url)) return new RelayRoute { HttpUrl = url.TrimEnd('/'), Password = Text(row, "password") };
            }
            throw new InvalidOperationException("No HTTP relay is available.");
        }

        public async Task<IReadOnlyList<TaskRow>> GetTasksAsync(string token, MachineRow machine, RelayRoute relay)
        {
            var response = await SendAgentAsync(HttpMethod.Get, machine, relay, "/tasks", token);
            await EnsureSuccess(response, "Tasks unavailable");
            var json = JsonObject.Parse(await response.Content.ReadAsStringAsync());
            var rows = new List<TaskRow>();
            if (!json.ContainsKey("tasks") || json["tasks"].ValueType != JsonValueType.Array) return rows;
            foreach (var value in json["tasks"].GetArray())
            {
                if (value.ValueType != JsonValueType.Object) continue;
                var item = value.GetObject();
                var verification = item.ContainsKey("verification") && item["verification"].ValueType == JsonValueType.Object
                    ? item["verification"].GetObject() : null;
                var verificationStatus = verification == null ? "" : Text(verification, "status");
                var clip = verification == null ? "" : Text(verification, "videoClipId");
                if (string.IsNullOrWhiteSpace(clip)) clip = Text(item, "videoClipId");
                rows.Add(new TaskRow
                {
                    Id = Text(item, "id"),
                    Title = string.IsNullOrWhiteSpace(Text(item, "title")) ? "Untitled task" : Text(item, "title"),
                    Status = Text(item, "status"),
                    Verification = verificationStatus == "passed"
                        ? "browser verified " + Number(verification, "passed") + "/" + Number(verification, "total")
                        : verificationStatus == "failed" ? "browser verification failed"
                        : verificationStatus == "running" ? "browser verification running" : "",
                    VideoClipId = clip,
                });
            }
            return rows;
        }

        public async Task<StorageFile> DownloadVerificationVideoAsync(string token, MachineRow machine, RelayRoute relay, string clipId)
        {
            var response = await SendAgentAsync(HttpMethod.Get, machine, relay, "/vibing/preview/clip/" + Uri.EscapeDataString(clipId), token);
            await EnsureSuccess(response, "Browser proof video unavailable");
            var length = response.Content.Headers.ContentLength;
            if (length.HasValue && length.Value > 512L * 1024L * 1024L) throw new InvalidOperationException("Browser proof video is too large for this Xbox.");
            var file = await ApplicationData.Current.TemporaryFolder.CreateFileAsync("yaver-proof-" + Guid.NewGuid().ToString("N") + ".mp4", CreationCollisionOption.FailIfExists);
            using (var input = await response.Content.ReadAsStreamAsync())
            using (var output = await file.OpenStreamForWriteAsync())
            {
                await input.CopyToAsync(output);
            }
            return file;
        }

        private async Task<HttpResponseMessage> SendPlatformAsync(HttpMethod method, string path, string token)
        {
            var request = new HttpRequestMessage(method, ApiOrigin + path);
            request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", token);
            request.Headers.Add("X-Yaver-Surface", "xbox");
            return await http.SendAsync(request);
        }

        private async Task<HttpResponseMessage> SendAgentAsync(HttpMethod method, MachineRow machine, RelayRoute relay, string path, string token)
        {
            if (machine == null || string.IsNullOrWhiteSpace(machine.DeviceId)) throw new InvalidOperationException("This machine has no device identity.");
            var url = relay.HttpUrl + "/d/" + Uri.EscapeDataString(machine.DeviceId) + path;
            var request = new HttpRequestMessage(method, url);
            request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", token);
            request.Headers.Add("X-Yaver-Surface", "xbox");
            if (!string.IsNullOrWhiteSpace(relay.Password)) request.Headers.Add("X-Relay-Password", relay.Password);
            return await http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead);
        }

        private static StringContent Json(JsonObject body) => new StringContent(body.Stringify(), Encoding.UTF8, "application/json");
        private static string Text(JsonObject value, string key) =>
            value.ContainsKey(key) && value[key].ValueType == JsonValueType.String ? value[key].GetString() : "";
        private static int Number(JsonObject value, string key) =>
            value != null && value.ContainsKey(key) && value[key].ValueType == JsonValueType.Number ? (int)value[key].GetNumber() : 0;
        private static async Task EnsureSuccess(HttpResponseMessage response, string message)
        {
            if ((int)response.StatusCode == 429)
            {
                var wait = response.Headers.RetryAfter?.Delta;
                var suffix = wait.HasValue ? " Wait " + Math.Max(1, (int)Math.Ceiling(wait.Value.TotalSeconds)) + " seconds and retry." : " Wait a moment and retry.";
                throw new HttpRequestException("Too many sign-in attempts." + suffix);
            }
            if (!response.IsSuccessStatusCode) throw new HttpRequestException(message + " (HTTP " + (int)response.StatusCode + ")");
            await Task.CompletedTask;
        }
    }

    static class EnumerableExtensions
    {
        public static IEnumerable<string> WhereNotEmpty(this IEnumerable<string> values)
        {
            foreach (var value in values) if (!string.IsNullOrWhiteSpace(value)) yield return value;
        }
    }
}
