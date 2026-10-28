// CMS parsing is evidence, not a Windows trust verdict. The caller must also
// require Windows Authenticode and SDK SignTool /pa /all /tw to succeed.
using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;
using System.Security.Cryptography.Pkcs;
using System.Security.Cryptography.X509Certificates;
using System.Text;

public static class SparrowAuthenticode
{
    sealed class Der
    {
        public byte Tag; public byte[] Data;
        public List<Der> Children() {
            var list = new List<Der>(); int p = 0;
            while (p < Data.Length) list.Add(Read(Data, ref p)); return list;
        }
        public static Der Read(byte[] b, ref int p) {
            if (p + 2 > b.Length) throw new InvalidDataException("Truncated DER");
            byte tag = b[p++]; int n = b[p++];
            if (n >= 128) {
                int count = n & 127; n = 0;
                if (count == 0 || count > 4 || p + count > b.Length) throw new InvalidDataException("DER length");
                for (int i = 0; i < count; i++) n = checked(n * 256 + b[p++]);
            }
            if (n < 0 || n > b.Length - p) throw new InvalidDataException("DER bounds");
            var data = new byte[n]; Buffer.BlockCopy(b, p, data, 0, n); p += n;
            return new Der { Tag = tag, Data = data };
        }
        public string Oid() {
            if (Tag != 6 || Data.Length == 0) throw new InvalidDataException("Expected OID");
            var s = new StringBuilder((Data[0] / 40) + "." + (Data[0] % 40)); long v = 0;
            for (int i = 1; i < Data.Length; i++) {
                v = checked(v * 128 + (Data[i] & 127));
                if ((Data[i] & 128) == 0) { s.Append('.').Append(v); v = 0; }
            }
            if ((Data[Data.Length - 1] & 128) != 0) throw new InvalidDataException("OID bounds");
            return s.ToString();
        }
    }
    static Der Root(byte[] b) { int p = 0; var d = Der.Read(b, ref p); if (p != b.Length) throw new InvalidDataException("Trailing DER"); return d; }
    static HashAlgorithm StrongHash(string oid) {
        switch (oid) {
            case "2.16.840.1.101.3.4.2.1": return SHA256.Create();
            case "2.16.840.1.101.3.4.2.2": return SHA384.Create();
            case "2.16.840.1.101.3.4.2.3": return SHA512.Create();
            default: throw new InvalidDataException("Unapproved digest OID: " + oid);
        }
    }
    static int SecurityDirectory(byte[] b) {
        if (b.Length < 256 || b[0] != 77 || b[1] != 90) throw new InvalidDataException("Not PE");
        int pe = BitConverter.ToInt32(b, 60);
        if (pe < 64 || pe > b.Length - 160 || BitConverter.ToUInt32(b, pe) != 0x4550) throw new InvalidDataException("PE header");
        int optional = pe + 24; ushort magic = BitConverter.ToUInt16(b, optional);
        if (magic != 0x20b && magic != 0x10b) throw new InvalidDataException("PE format");
        int directory = optional + (magic == 0x20b ? 112 : 96);
        if (directory + 40 > b.Length) throw new InvalidDataException("PE directories");
        return directory + 32;
    }
    static byte[] Certificate(byte[] b) {
        int d = SecurityDirectory(b), offset = BitConverter.ToInt32(b, d), size = BitConverter.ToInt32(b, d + 4);
        if (offset < d + 8 || size < 8 || offset > b.Length - size || offset + size != b.Length) throw new InvalidDataException("Missing or malformed embedded signature");
        int length = BitConverter.ToInt32(b, offset);
        if (length < 8 || length > size || ((length + 7) & ~7) != size || BitConverter.ToUInt16(b, offset + 4) != 0x200 || BitConverter.ToUInt16(b, offset + 6) != 2) throw new InvalidDataException("Unsupported certificate table");
        byte[] cms = new byte[length - 8]; Buffer.BlockCopy(b, offset + 8, cms, 0, cms.Length);
        // SDK SignTool may include zero alignment padding in dwLength itself.
        // Accept only <=7 zero bytes outside the one DER ContentInfo.
        int end = 0; if (Der.Read(cms, ref end).Tag != 0x30 || cms.Length - end > 7) throw new InvalidDataException("Invalid CMS padding");
        for (int i = end; i < cms.Length; i++) if (cms[i] != 0) throw new InvalidDataException("Nonzero CMS padding");
        Array.Resize(ref cms, end); return cms;
    }
    // Signing transaction invariant only, NOT an Authenticode trust check.
    // Normalize checksum/security directory and <=7 zero alignment bytes.
    public static string PayloadHash(string path) {
        byte[] b = File.ReadAllBytes(path); int d = SecurityDirectory(b);
        int offset = BitConverter.ToInt32(b, d), size = BitConverter.ToInt32(b, d + 4), length = b.Length;
        if (offset != 0 || size != 0) { Certificate(b); length = offset; }
        byte[] normalized = new byte[checked((length + 7) & ~7)]; Buffer.BlockCopy(b, 0, normalized, 0, length);
        Array.Clear(normalized, BitConverter.ToInt32(b, 60) + 24 + 64, 4); Array.Clear(normalized, d, 8);
        using (var hash = SHA256.Create()) return BitConverter.ToString(hash.ComputeHash(normalized)).Replace("-", "");
    }
    public static string CheckEvidence(string path, string expectedSigner, string expectedTimestamp, string expectedPolicy) {
        byte[] raw = Certificate(File.ReadAllBytes(path)); var cms = new SignedCms(); cms.Decode(raw);
        if (cms.SignerInfos.Count != 1) throw new InvalidDataException("Exactly one signer is required");
        var signer = cms.SignerInfos[0];
        if (signer.Certificate == null || !String.Equals(signer.Certificate.Thumbprint, expectedSigner, StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("Signer mismatch");
        using (StrongHash(signer.DigestAlgorithm.Value)) { }
        if (cms.ContentInfo.ContentType.Value != "1.3.6.1.4.1.311.2.1.4") throw new InvalidDataException("Not Authenticode content");
        string fileDigest = Root(cms.ContentInfo.Content).Children()[1].Children()[0].Children()[0].Oid();
        using (StrongHash(fileDigest)) { }
        SignedCms token = null;
        foreach (CryptographicAttributeObject attr in signer.UnsignedAttributes) {
            if (attr.Oid.Value != "1.3.6.1.4.1.311.3.3.1" || attr.Values.Count != 1 || token != null) throw new InvalidDataException("Unsupported or ambiguous unsigned attribute");
            token = new SignedCms(); token.Decode(attr.Values[0].RawData);
        }
        if (token == null) throw new InvalidDataException("Missing RFC3161 timestamp evidence");
        if (token.ContentInfo.ContentType.Value != "1.2.840.113549.1.9.16.1.4" || token.SignerInfos.Count != 1) throw new InvalidDataException("Invalid RFC3161 token");
        try { token.CheckSignature(true); } // mathematical integrity, not trust
        catch (CryptographicException e) { throw new InvalidDataException("Invalid timestamp token signature", e); }
        var tsa = token.SignerInfos[0]; using (StrongHash(tsa.DigestAlgorithm.Value)) { }
        if (tsa.Certificate == null || !String.Equals(tsa.Certificate.Thumbprint, expectedTimestamp, StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("Timestamp signer mismatch");
        var tst = Root(token.ContentInfo.Content).Children(); var imprint = tst[2].Children();
        if (tst[1].Oid() != expectedPolicy) throw new InvalidDataException("Timestamp policy OID mismatch");
        string imprintOid = imprint[0].Children()[0].Oid();
        // Extract encryptedDigest from the actual CMS SignerInfo.
        var signedData = Root(raw).Children()[1].Children()[0].Children();
        var fields = signedData[signedData.Count - 1].Children()[0].Children();
        byte[] signature = fields[fields[3].Tag == 0xa0 ? 5 : 4].Data;
        using (var hash = StrongHash(imprintOid)) {
            if (Convert.ToBase64String(hash.ComputeHash(signature)) != Convert.ToBase64String(imprint[1].Data)) throw new InvalidDataException("Timestamp message imprint mismatch");
        }
        if (tst[4].Tag != 24) throw new InvalidDataException("Missing timestamp time");
        DateTime when;
        if (!DateTime.TryParseExact(Encoding.ASCII.GetString(tst[4].Data), new string[] { "yyyyMMddHHmmss'Z'", "yyyyMMddHHmmss.FFFFFFF'Z'" }, System.Globalization.CultureInfo.InvariantCulture, System.Globalization.DateTimeStyles.AssumeUniversal | System.Globalization.DateTimeStyles.AdjustToUniversal, out when)) throw new InvalidDataException("Invalid timestamp time");
        if (when > DateTime.UtcNow.AddMinutes(5) || when < signer.Certificate.NotBefore.ToUniversalTime() || when > signer.Certificate.NotAfter.ToUniversalTime()) throw new InvalidDataException("Timestamp outside signer validity");
        bool timestampEKU = false;
        foreach (X509Extension ext in tsa.Certificate.Extensions) {
            var eku = ext as X509EnhancedKeyUsageExtension;
            if (eku != null && eku.Critical && eku.EnhancedKeyUsages.Count == 1 && eku.EnhancedKeyUsages[0].Value == "1.3.6.1.5.5.7.3.8") timestampEKU = true;
        }
        if (!timestampEKU) throw new InvalidDataException("Timestamp certificate requires exclusive critical timestamp EKU");
        // A matching timestamp certificate is not trust. Independently require
        // its timestamp-EKU chain at genTime, with online revocation and no
        // ignored chain errors. Windows SignTool must also bind/validate it.
        using (var chain = new X509Chain()) {
            chain.ChainPolicy.VerificationTime = when;
            chain.ChainPolicy.RevocationMode = X509RevocationMode.Online;
            chain.ChainPolicy.RevocationFlag = X509RevocationFlag.ExcludeRoot;
            chain.ChainPolicy.VerificationFlags = X509VerificationFlags.NoFlag;
            chain.ChainPolicy.UrlRetrievalTimeout = TimeSpan.FromSeconds(30);
            chain.ChainPolicy.ApplicationPolicy.Add(new Oid("1.3.6.1.5.5.7.3.8"));
            chain.ChainPolicy.ExtraStore.AddRange(token.Certificates);
            if (!chain.Build(tsa.Certificate)) throw new InvalidDataException("Timestamp certificate chain is not trusted or revocation could not be checked");
        }
        return "fileDigest=" + fileDigest + " timestampImprint=" + imprintOid + " timestampUTC=" + when.ToString("o");
    }
}
