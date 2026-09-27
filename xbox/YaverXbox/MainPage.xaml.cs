using System;
using System.Threading;
using System.Threading.Tasks;
using Windows.Security.Credentials;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;
using YaverXbox.Services;

namespace YaverXbox
{
    public sealed partial class MainPage : Page
    {
        private const string VaultResource = "io.yaver.xbox.session";
        private readonly YaverApiClient api = new YaverApiClient();
        private CancellationTokenSource polling;
        private string token;
        private MachineRow selectedMachine;
        private RelayRoute relay;

        public MainPage()
        {
            InitializeComponent();
            Loaded += async (_, __) => await RestoreAsync();
        }

        public void Pause() => polling?.Cancel();
        public async void Resume()
        {
            if (!string.IsNullOrWhiteSpace(token)) await LoadMachinesAsync();
        }

        private async Task RestoreAsync()
        {
            try
            {
                var vault = new PasswordVault();
                var credential = vault.Retrieve(VaultResource, "session");
                credential.RetrievePassword();
                token = credential.Password;
            }
            catch { token = null; }

            if (string.IsNullOrWhiteSpace(token))
            {
                ShowSignedOut();
                StartSignInButton.Focus(FocusState.Programmatic);
                return;
            }
            await LoadMachinesAsync();
        }

        private async void StartSignIn_Click(object sender, RoutedEventArgs e)
        {
            polling?.Cancel();
            polling = new CancellationTokenSource();
            SetStatus("Requesting a secure device code…");
            try
            {
                var start = await api.StartDeviceCodeAsync();
                UserCodeText.Text = start.UserCode;
                VerifyUrlText.Text = "Open yaver.io/auth/device and enter this code";
                SetStatus("Waiting for approval on your trusted device…");
                token = await api.WaitForTokenAsync(start.DeviceCode, polling.Token);
                SaveToken(token);
                await LoadMachinesAsync();
            }
            catch (OperationCanceledException) { SetStatus("Sign-in paused."); }
            catch (Exception ex) { SetStatus("Sign-in unavailable: " + ex.Message); }
        }

        private async void RefreshMachines_Click(object sender, RoutedEventArgs e) => await LoadMachinesAsync();

        private async void MachineList_ItemClick(object sender, ItemClickEventArgs e)
        {
            selectedMachine = e.ClickedItem as MachineRow;
            if (selectedMachine == null) return;
            SetStatus("Loading tasks from " + selectedMachine.Name + "…");
            try
            {
                relay = relay ?? await api.GetRelayAsync(token);
                TaskList.ItemsSource = await api.GetTasksAsync(token, selectedMachine, relay);
                SetStatus(TaskList.Items.Count + " task(s) loaded. Select a verified task to watch its browser proof.");
                TaskList.Focus(FocusState.Programmatic);
            }
            catch (Exception ex) { SetStatus("Tasks unavailable: " + ex.Message); }
        }

        private async void TaskList_ItemClick(object sender, ItemClickEventArgs e)
        {
            var task = e.ClickedItem as TaskRow;
            if (task == null) return;
            if (string.IsNullOrWhiteSpace(task.VideoClipId))
            {
                SetStatus(string.IsNullOrWhiteSpace(task.Verification) ? "This task has no browser verification yet." : task.Verification + ". No recording was produced.");
                return;
            }
            try
            {
                SetStatus("Loading authenticated browser proof…");
                var file = await api.DownloadVerificationVideoAsync(token, selectedMachine, relay, task.VideoClipId);
                var stream = await file.OpenAsync(Windows.Storage.FileAccessMode.Read);
                ProofPlayer.SetSource(stream, "video/mp4");
                ProofPlayer.Play();
                SetStatus("Playing browser verification for " + task.Title + ".");
            }
            catch (Exception ex) { SetStatus("Browser proof unavailable: " + ex.Message); }
        }

        private async Task LoadMachinesAsync()
        {
            SetStatus("Checking your machines…");
            try
            {
                MachineList.ItemsSource = await api.GetMachinesAsync(token);
                SignedOutPanel.Visibility = Visibility.Collapsed;
                SignedInPanel.Visibility = Visibility.Visible;
                SetStatus("Connected securely. " + MachineList.Items.Count + " machine(s) available.");
                MachineList.Focus(FocusState.Programmatic);
            }
            catch (UnauthorizedAccessException)
            {
                ForgetToken();
                ShowSignedOut();
                SetStatus("Session expired. Approve this Xbox again.");
            }
            catch (Exception ex) { SetStatus("Machines unavailable: " + ex.Message); }
        }

        private void SignOut_Click(object sender, RoutedEventArgs e)
        {
            polling?.Cancel();
            ForgetToken();
            ShowSignedOut();
            SetStatus("Signed out. This Xbox no longer holds a Yaver session.");
            StartSignInButton.Focus(FocusState.Programmatic);
        }

        private void SaveToken(string value)
        {
            ForgetToken();
            new PasswordVault().Add(new PasswordCredential(VaultResource, "session", value));
        }

        private void ForgetToken()
        {
            token = null;
            try
            {
                var vault = new PasswordVault();
                vault.Remove(vault.Retrieve(VaultResource, "session"));
            }
            catch { }
        }

        private void ShowSignedOut()
        {
            SignedOutPanel.Visibility = Visibility.Visible;
            SignedInPanel.Visibility = Visibility.Collapsed;
            MachineList.ItemsSource = null;
            TaskList.ItemsSource = null;
            ProofPlayer.Stop();
            selectedMachine = null;
            relay = null;
        }

        private void SetStatus(string text) => StatusText.Text = text;
    }
}
