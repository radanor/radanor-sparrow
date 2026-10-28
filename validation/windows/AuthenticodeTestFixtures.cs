// Acceptance fixtures only: deliberately untrusted RFC3161 evidence. No keys
// are serialized. This helper is never loaded by a production signing script.
using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Security.Cryptography.Pkcs;
using System.Security.Cryptography.X509Certificates;
using System.Text;

public static class AuthenticodeTestFixtures
{
    static byte[] Join(params byte[][] parts) { return parts.SelectMany(p => p).ToArray(); }
    static byte[] T(byte tag, params byte[][] parts) {
        byte[] b = Join(parts); var h = new List<byte> { tag };
        if (b.Length < 128) h.Add((byte)b.Length);
        else { var length = new List<byte>(); int n = b.Length; while (n != 0) { length.Insert(0, (byte)n); n >>= 8; } h.Add((byte)(128 + length.Count)); h.AddRange(length); }
        return Join(h.ToArray(), b);
    }
    static byte[] Hex(string s) { return Enumerable.Range(0, s.Length / 2).Select(i => Convert.ToByte(s.Substring(i * 2, 2), 16)).ToArray(); }
    static List<byte[]> Children(byte[] b) {
        int p = 1, n = b[p++]; if (n >= 128) p += n & 127;
        var list = new List<byte[]>();
        while (p < b.Length) {
            if (p + 2 > b.Length) throw new InvalidDataException("Fixture DER truncated tag=" + b[0] + " length=" + b.Length + " offset=" + p);
            int start = p++; n = b[p++];
            if (n >= 128) { int count = n & 127; n = 0; while (count-- > 0) n = n * 256 + b[p++]; }
            if (n > b.Length - p) throw new InvalidDataException("Fixture DER invalid length tag=" + b[0] + " length=" + b.Length + " child=" + n + " offset=" + p);
            p += n; list.Add(b.Skip(start).Take(p - start).ToArray());
        }
        return list;
    }
    public static void Timestamp(string input, string output, X509Certificate2 tsa, string mode) {
        byte[] pe = File.ReadAllBytes(input);
        int optional = BitConverter.ToInt32(pe, 60) + 24;
        int directory = optional + (BitConverter.ToUInt16(pe, optional) == 0x20b ? 112 : 96) + 32;
        int offset = BitConverter.ToInt32(pe, directory), length = BitConverter.ToInt32(pe, offset);
        byte[] raw = pe.Skip(offset + 8).Take(length - 8).ToArray();
        var original = new SignedCms(); original.Decode(raw); raw = original.Encode();
        byte[] signature = original.SignerInfos[0].GetSignature();
        byte[] imprint; using (var hash = SHA256.Create()) imprint = hash.ComputeHash(signature);
        if (mode == "imprint") imprint[0] ^= 1;
        DateTime time = mode == "time" ? DateTime.UtcNow.AddYears(2) : DateTime.UtcNow;
        byte[] info = T(0x30, Hex("020101"), Hex("06022A03"),
            T(0x30, T(0x30, Hex("0609608648016503040201"), Hex("0500")), T(4, imprint)),
            Hex("020101"), T(24, Encoding.ASCII.GetBytes(time.ToString("yyyyMMddHHmmss'Z'"))));
        var timestamp = new SignedCms(new ContentInfo(new Oid("1.2.840.113549.1.9.16.1.4"), info));
        var signer = new CmsSigner(tsa); signer.DigestAlgorithm = new Oid("2.16.840.1.101.3.4.2.1"); timestamp.ComputeSignature(signer);
        byte[] token = timestamp.Encode(); if (mode == "signature") token[token.Length - 1] ^= 1;
        byte[] attr = T(0x30, Hex("060A2B060104018237030301"), T(0x31, token));
        var root = Children(raw); var sd = Children(Children(root[1])[0]);
        var signers = Children(sd[sd.Count - 1]); var fields = Children(signers[0]);
        fields.Add(T(0xa1, attr)); signers[0] = T(0x30, fields.ToArray());
        sd[sd.Count - 1] = T(0x31, signers.ToArray()); root[1] = T(0xa0, T(0x30, sd.ToArray()));
        byte[] cms = T(0x30, root.ToArray()); int certLength = cms.Length + 8, padded = (certLength + 7) & ~7;
        byte[] result = new byte[offset + padded]; Buffer.BlockCopy(pe, 0, result, 0, offset);
        Buffer.BlockCopy(BitConverter.GetBytes(padded), 0, result, directory + 4, 4);
        Buffer.BlockCopy(BitConverter.GetBytes(certLength), 0, result, offset, 4);
        result[offset + 5] = 2; result[offset + 6] = 2;
        Buffer.BlockCopy(cms, 0, result, offset + 8, cms.Length); File.WriteAllBytes(output, result);
    }
}
