using Windows.ApplicationModel;
using Windows.ApplicationModel.Activation;
using Windows.UI.ViewManagement;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;

namespace YaverXbox
{
    sealed partial class App : Application
    {
        public App()
        {
            InitializeComponent();
            Suspending += OnSuspending;
            Resuming += OnResuming;
        }

        protected override void OnLaunched(LaunchActivatedEventArgs e)
        {
            ApplicationView.PreferredLaunchWindowingMode = ApplicationViewWindowingMode.FullScreen;
            var frame = Window.Current.Content as Frame ?? new Frame();
            if (frame.Content == null) frame.Navigate(typeof(MainPage));
            Window.Current.Content = frame;
            Window.Current.Activate();
        }

        private void OnSuspending(object sender, SuspendingEventArgs e)
        {
            var deferral = e.SuspendingOperation.GetDeferral();
            ((Window.Current.Content as Frame)?.Content as MainPage)?.Pause();
            deferral.Complete();
        }

        private void OnResuming(object sender, object e)
        {
            ((Window.Current.Content as Frame)?.Content as MainPage)?.Resume();
        }
    }
}
