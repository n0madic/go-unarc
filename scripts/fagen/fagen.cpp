// fagen: test-data generator built from FreeArc's own compression library (the Compression/ folder of the C unarc).
// It is test tooling only: the Go unarc never links it.
//
//   fagen c <method>                        compress stdin to stdout; "a+b+c" runs a, then b, then c
//   fagen d <method>                        decompress stdin to stdout with the C library (reference decoder)
//   fagen pbkdf2 <pwd> <salthex> <n> <len>  print PBKDF2-HMAC-SHA512 as hex
//   fagen crypt <alg> <pwd>                 print two lines, FreeArc 0.67 style:
//                                           1) method that encrypts (key/iv already "mangled" the way 0.67 reads them)
//                                           2) method stored in the archive (salt, check code, iv)
//   fagen mem <method>                      print GetDecompressionMem(method)
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <string>
#include <vector>

#include "Environment.h"
#include "Compression/Compression.h"

extern "C" int systemRandomData (char *rand_buf, int rand_size);
int CompressionService (char *method, char *what, int param, void *data, CALLBACK_FUNC *callback);

static std::vector<unsigned char> in_buf, out_buf;
static size_t in_pos;

static int mem_callback (const char *what, void *buf, int size, void *)
{
  if (strcmp (what, "read") == 0) {
    size_t n = in_buf.size() - in_pos;
    if ((size_t)size < n)  n = size;
    memcpy (buf, in_buf.data() + in_pos, n);
    in_pos += n;
    return (int) n;
  }
  if (strcmp (what, "write") == 0) {
    out_buf.insert (out_buf.end(), (unsigned char*)buf, (unsigned char*)buf + size);
    return size;
  }
  return FREEARC_ERRCODE_NOT_IMPLEMENTED;
}

static void read_stdin (void)
{
  unsigned char tmp[1<<16];
  size_t n;
  while ((n = fread (tmp, 1, sizeof tmp, stdin)) > 0)
    in_buf.insert (in_buf.end(), tmp, tmp + n);
}

static void write_stdout (void)
{
  if (!out_buf.empty() && fwrite (out_buf.data(), 1, out_buf.size(), stdout) != out_buf.size()) {
    fprintf (stderr, "fagen: write failed\n");
    exit (1);
  }
}

static std::string hex (const unsigned char *p, int n)
{
  static const char digits[] = "0123456789abcdef";
  std::string s;
  for (int i = 0; i < n; i++)  s += digits[p[i] >> 4], s += digits[p[i] & 15];
  return s;
}

static int hexval (char c)        {return c >= '0' && c <= '9'? c - '0' : (c | 32) - 'a' + 10;}
static int buggy_hexval (char c)  {return c >= '0' && c <= '9'? c - '0' : (c | 32) - 'a';}

static std::vector<unsigned char> unhex (const char *s, int (*val)(char))
{
  std::vector<unsigned char> v;
  for (; s[0] && s[1]; s += 2)  v.push_back ((unsigned char)(val (s[0]) * 16 + val (s[1])));
  return v;
}

static int cmd_compress (char *method)
{
  read_stdin();
  std::string all = method;
  size_t start = 0;
  for (;;) {
    size_t plus = all.find ('+', start);
    std::string m = all.substr (start, plus == std::string::npos? std::string::npos : plus - start);
    in_pos = 0;  out_buf.clear();
    int rc = Compress ((char*) m.c_str(), mem_callback, NULL);
    if (rc < 0) {fprintf (stderr, "fagen: compress %s failed: %d\n", m.c_str(), rc);  return 1;}
    if (plus == std::string::npos)  break;
    in_buf.swap (out_buf);
    start = plus + 1;
  }
  write_stdout();
  return 0;
}

static int cmd_decompress (char *method)
{
  read_stdin();
  in_pos = 0;  out_buf.clear();
  int rc = strchr (method, '+')? MultiDecompress (method, mem_callback, NULL) : Decompress (method, mem_callback, NULL);
  write_stdout();
  if (rc < 0) {fprintf (stderr, "fagen: decompress %s failed: %d\n", method, rc);  return 1;}
  return 0;
}

static int cmd_pbkdf2 (char *pwd, char *salthex, int n, int len)
{
  std::vector<unsigned char> salt = unhex (salthex, hexval), key (len);
  Pbkdf2Hmac ((const BYTE*) pwd, strlen (pwd), salt.data(), salt.size(), n, key.data(), len);
  printf ("%s\n", hex (key.data(), len).c_str());
  return 0;
}

// FreeArc 0.67 (Encryption.hs) derives key+check code with PBKDF2 and hands the C library ":k<hex key>:i<hex iv>";
// the 0.67 C library reads k and i with a hex decoder that maps a..f to 0..5. The stored method keeps the real
// salt, check code and iv in correct hex. Our compress() reads k/i with the correct decoder, so we pass it the
// bytes 0.67 would have ended up with.
static int cmd_crypt (char *alg, char *pwd)
{
  int keySize = CompressionService (alg, (char*)"keySize", 0, NULL, NULL);
  int ivSize  = CompressionService (alg, (char*)"ivSize", 0, NULL, NULL);
  int n       = CompressionService (alg, (char*)"numIterations", 0, NULL, NULL);
  if (keySize <= 0 || ivSize <= 0 || n <= 0) {fprintf (stderr, "fagen: not an encryption method: %s\n", alg);  return 1;}
  std::vector<unsigned char> salt (keySize), iv (ivSize), derived (keySize + 2);
  if (systemRandomData ((char*) salt.data(), keySize) != keySize || systemRandomData ((char*) iv.data(), ivSize) != ivSize)
    {fprintf (stderr, "fagen: no random data\n");  return 1;}
  Pbkdf2Hmac ((const BYTE*) pwd, strlen (pwd), salt.data(), keySize, n, derived.data(), keySize + 2);
  std::vector<unsigned char> key = unhex (hex (derived.data(), keySize).c_str(), buggy_hexval);
  std::vector<unsigned char> ivm = unhex (hex (iv.data(), ivSize).c_str(), buggy_hexval);
  printf ("%s:k%s:i%s\n", alg, hex (key.data(), keySize).c_str(), hex (ivm.data(), ivSize).c_str());
  printf ("%s:s%s:c%s:i%s\n", alg, hex (salt.data(), keySize).c_str(), hex (derived.data() + keySize, 2).c_str(),
          hex (iv.data(), ivSize).c_str());
  return 0;
}

int main (int argc, char **argv)
{
  if (argc == 3 && strcmp (argv[1], "c") == 0)       return cmd_compress (argv[2]);
  if (argc == 3 && strcmp (argv[1], "d") == 0)       return cmd_decompress (argv[2]);
  if (argc == 6 && strcmp (argv[1], "pbkdf2") == 0)  return cmd_pbkdf2 (argv[2], argv[3], atoi (argv[4]), atoi (argv[5]));
  if (argc == 4 && strcmp (argv[1], "crypt") == 0)   return cmd_crypt (argv[2], argv[3]);
  if (argc == 3 && strcmp (argv[1], "mem") == 0)     {printf ("%u\n", GetDecompressionMem (argv[2]));  return 0;}
  fprintf (stderr, "usage: fagen c|d <method> | pbkdf2 <pwd> <salthex> <n> <len> | crypt <alg> <pwd> | mem <method>\n");
  return 2;
}
