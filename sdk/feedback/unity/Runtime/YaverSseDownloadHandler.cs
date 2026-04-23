using System;
using System.Collections.Generic;
using System.Text;
using UnityEngine.Networking;

namespace Yaver.Feedback
{
    internal sealed class YaverSseDownloadHandler : DownloadHandlerScript
    {
        private readonly Action<string> _onData;
        private readonly List<byte> _lineBuffer = new List<byte>(1024);

        public YaverSseDownloadHandler(Action<string> onData) : base(new byte[1024])
        {
            _onData = onData;
        }

        protected override bool ReceiveData(byte[] data, int dataLength)
        {
            if (data == null || dataLength <= 0)
            {
                return true;
            }

            for (var i = 0; i < dataLength; i++)
            {
                var value = data[i];
                if (value == '\n')
                {
                    FlushLine();
                    continue;
                }

                if (value != '\r')
                {
                    _lineBuffer.Add(value);
                }
            }

            return true;
        }

        protected override void CompleteContent()
        {
            FlushLine();
            base.CompleteContent();
        }

        private void FlushLine()
        {
            if (_lineBuffer.Count == 0)
            {
                return;
            }

            var line = Encoding.UTF8.GetString(_lineBuffer.ToArray());
            _lineBuffer.Clear();
            if (!line.StartsWith("data: ", StringComparison.Ordinal))
            {
                return;
            }

            var payload = line.Substring(6);
            if (!string.IsNullOrEmpty(payload))
            {
                _onData?.Invoke(payload);
            }
        }
    }
}
