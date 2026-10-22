# Rencana pengujian QA: aplikasi mobile ReturnIt

Versi bahasa Inggris: `qa-test-plan.md` (isinya sama; jika ada beda, samakan keduanya).

Test case manual untuk aplikasi Android (iOS menyusul) terhadap backend yang asli. Jalankan seluruh daftar sebelum setiap
rilis, dan jalankan kasus "Smoke" setiap ada build baru. Perilaku yang diharapkan berasal dari `01-mobile-app-guide.md`,
`02-screens-and-api-map.md` dan kebutuhan di `05-requirements/`; kode error ada di `03-openapi.yaml`.

Tandai tiap kasus **Lulus / Gagal / Terblokir** dan catat nomor build. Kasus yang gagal dijadikan laporan bug berisi langkah,
hasil yang diharapkan dan hasil sebenarnya, tangkapan layar, dan (jika error dari server) `requestId` dari isi respons.

## 1. Lingkungan pengujian

| Lingkungan | API | Server login | Dipakai untuk |
|---|---|---|---|
| Lokal (dev) | `http://localhost:8080/api/v1` (emulator: `adb reverse tcp:8080 tcp:8080`, begitu juga `8180`) | `http://localhost:8180/realms/returnit` | Pengembangan harian |
| Staging | `https://api.<domain>/api/v1` | `https://auth.<domain>/realms/returnit` | Tes rilis, HP asli |

Jangan memakai `10.0.2.2` atau IP LAN untuk server login: issuer token jadi tidak cocok dan semua panggilan API menghasilkan 401.

**Akun tes (hanya dev lokal):** `alice` / `alice-dev-pass`, `bob` / `bob-dev-pass`. Akun baru yang dibuat di Keycloak harus
punya nama depan dan nama belakang, kalau tidak tidak bisa login. Di staging, daftarkan akun baru dengan e-mail yang kamu
kuasai. Jangan pernah menguji dengan data orang sungguhan.

**Perangkat yang perlu dicoba:** satu HP Android kelas bawah dan satu yang baru (Android 10 dan versi terbaru), satu layar
kecil dan satu besar, satu HP berbahasa Indonesia dan satu berbahasa Inggris. Tes push wajib di HP asli.

**Alat bantu:** kotak surat dev lokal `http://localhost:8025` (Mailpit); konsol MinIO `http://localhost:9001`
(`returnit` / `returnit_dev_secret`); `docker compose logs web worker --tail 50` untuk melihat alasan di sisi server.

**File tes** (siapkan sekali): JPEG di bawah 1 MiB, PNG, PDF, JPEG sekitar 9 MiB, file sekitar 12 MiB, file `.txt` yang diganti
namanya jadi `.jpg`, file kosong (0 byte), file dengan nama sangat panjang.

## 2. Smoke test (sekitar 10 menit, setiap build)

| ID | Langkah | Hasil yang diharapkan |
|---|---|---|
| S1 | Install dan buka aplikasi | Terbuka tanpa crash atau layar error merah |
| S2 | Login dengan akun tes | Browser membuka halaman login, kembali ke aplikasi, Beranda tampil |
| S3 | Tambah pembelian dengan tanggal akhir garansi | Muncul di daftar |
| S4 | Lampirkan struk JPEG | Upload berhasil, struk bisa dilihat di layar detail |
| S5 | Matikan paksa aplikasi, buka lagi | Masih login, data masih ada |
| S6 | Keluar (logout) | Kembali ke keadaan belum login |

## 3. Test case lengkap

### 3.1 Login, sesi, keluar

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| A1 | Login berhasil | Tekan login, isi kredensial yang benar | Kembali ke aplikasi, Beranda termuat |
| A2 | Password salah | Isi password yang salah | Error tampil di halaman login, tidak crash; setelah 5 kali salah akun dikunci sementara |
| A3 | Batal login | Tutup tab browser atau tekan kembali | Aplikasi tetap di layar belum login, bisa mencoba lagi |
| A4 | Daftar | Pakai "Register" di halaman login dengan e-mail baru | Akun terbuat; (staging) e-mail verifikasi masuk; bisa login setelah verifikasi |
| A5 | Lupa password | Pakai "Forgot password" | E-mail reset masuk dan password baru berfungsi |
| A6 | Token diperbarui diam-diam | Biarkan aplikasi terbuka lebih dari 6 menit (token hidup sekitar 5 menit), lalu buka daftar | Tidak ada permintaan login, data termuat |
| A7 | Sesi bertahan setelah restart | Hentikan paksa aplikasi, buka lagi | Masih login |
| A8 | Sesi kedaluwarsa | Login di HP. Lalu akhiri sesi pengguna di server: `./kc-user.sh logout <email>` (tim backend; atau konsol Keycloak, Users, Sessions, Sign out). Lanjutkan memakai aplikasi. | Sekitar 5 menit pertama aplikasi masih berjalan (access token yang sudah diterbitkan masih berlaku). Setelah kedaluwarsa, pembaruan token diam-diam ditolak (`invalid_grant`) dan aplikasi harus menampilkan layar login: tidak crash, tidak spinner tanpa akhir, tidak ada pesan error mentah. Setelah login ulang, data pengguna masih ada. Agar tidak menunggu, biarkan aplikasi di latar belakang 6 menit sebelum dipakai lagi. |
| A9 | Keluar | Pengaturan, keluar | Kembali ke belum login; perangkat dihapus dari server (`DELETE /devices/{id}`); login berikutnya mulai bersih |
| A10 | Dua pengguna di satu HP | Logout alice, login sebagai bob | Bob tidak melihat data alice (tidak ada daftar yang tertinggal di cache) |
| A11 | Login berbeda, e-mail sama | Login dengan identitas yang e-mailnya sudah milik akun lain | Aplikasi menampilkan pesan jelas dari `409 identity_conflict` ("hubungi dukungan"), bukan layar kosong |
| A12 | Tanpa jaringan saat login | Mode pesawat, tekan login | Pesan error ramah, bisa mencoba lagi nanti |

### 3.2 Pembelian: tambah, daftar, cari, filter

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| P1 | Tambah dengan semua field | Isi nama, toko, tanggal, durasi 12 bulan, harga, catatan | Terbuat; detail menampilkan akhir garansi yang benar (mulai + 12 bulan) |
| P2 | Tambah hanya field wajib | Hanya nama dan tanggal pembelian | Terbuat; garansi tampil "tidak diketahui" |
| P3 | Field wajib kosong | Biarkan nama kosong | Pesan validasi di tempat, tidak ada yang dikirim |
| P4 | Nama di batas maksimum | 160 karakter, lalu 161 | 160 diterima; 161 diblokir aplikasi atau tampil sebagai error validasi |
| P5 | Karakter khusus | Nama berisi emoji, tanda kutip, `<script>`, teks Indonesia | Tersimpan dan tampil persis seperti diketik, tampilan tidak rusak |
| P6 | Harga kasus tepi | 0, 1500000, 1500000.50, sangat besar, negatif | 0 dan angka valid diterima; negatif ditolak |
| P7 | Tanggal kasus tepi | Hari ini, tanggal lampau, 29 Februari tahun kabisat, akhir bulan + 1 bulan | Diterima; hitungan bulan menghasilkan tanggal valid (31 Jan + 1 bulan = akhir Februari) |
| P8 | Daftar terbaru dulu | Tambah tiga pembelian dengan tanggal berbeda | Urut berdasarkan tanggal pembelian, terbaru di atas |
| P9 | Paginasi | Buat 25+ pembelian, gulir | Halaman berikutnya termuat, tanpa duplikat atau yang terlewat |
| P10 | Cari | Ketik sebagian nama atau toko | Hanya yang cocok; mengosongkan kolom mengembalikan daftar |
| P11 | Filter Aktif | Pilih chip | Hanya pembelian dengan garansi yang masih berjalan |
| P12 | Filter Segera Berakhir | Tambah pembelian yang berakhir 10 hari lagi | Muncul; yang berakhir 60 hari lagi tidak |
| P13 | Filter Kedaluwarsa | Tambah pembelian yang garansinya berakhir kemarin | Muncul |
| P14 | Filter Tidak Diketahui | Tambah pembelian tanpa tanggal garansi | Muncul |
| P15 | Penghitung Beranda | Bandingkan tiga penghitung dengan filter daftar | Angkanya cocok |
| P16 | Keadaan kosong | Akun baru tanpa pembelian | Tampilan kosong yang ramah dengan tombol "tambah", bukan error |
| P17 | Tarik untuk muat ulang | Tambah pembelian dari perangkat lain atau Postman, tarik ke bawah | Item baru muncul |
| P18 | Status larut malam | Antara 00:00 dan 07:00 WIB, cek item yang berakhir hari ini/kemarin | Keterbatasan yang diketahui: server bisa menghitung telat satu hari; "sisa hari" di layar dihitung di HP dan harus benar |

### 3.3 Detail pembelian, ubah, hapus

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| D1 | Isi detail | Buka sebuah pembelian | Semua field tampil; sisa hari dan status benar |
| D2 | Ubah | Ganti toko lalu simpan | Perubahan tampil di detail dan daftar; **field lain tidak berubah** (aplikasi mengirim objek lengkap) |
| D3 | Ubah tidak menghapus catatan dan harga | Ubah hanya nama | Harga, catatan dan tanggal masih ada setelah disimpan |
| D4 | Mengosongkan field dengan sengaja | Hapus catatan lalu simpan | Catatan hilang |
| D5 | Hapus | Hapus pembelian dan konfirmasi | Hilang dari daftar, penghitung turun; file struk ikut terhapus |
| D6 | Batal hapus | Tekan hapus, lalu batal | Tidak ada yang terhapus |
| D7 | Membuka pembelian pengguna lain | Lewat Postman, minta id pembelian alice dengan token bob | `404`, tidak pernah datanya |
| D8 | Item terhapus dibuka dari layar lama | Hapus di perangkat lain, buka di perangkat ini | Ditangani dengan "tidak ditemukan" yang rapi, tidak crash |

### 3.4 Struk

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| R1 | Upload JPEG | Pilih JPEG | Berhasil, thumbnail atau nama file tampil |
| R2 | Upload PNG dan PDF | Ulangi | Keduanya berhasil |
| R3 | Lihat struk | Buka struk | File terbuka/terunduh dari tautan bertanda tangan; berfungsi tepat setelah ditekan |
| R4 | Ganti | Upload file lain ke pembelian yang sama | File lama diganti, hanya yang baru yang bisa dilihat |
| R5 | Hapus | Hapus struk | Detail tidak lagi menampilkan struk; melihat struk menampilkan keadaan "tidak ada" |
| R6 | File 9 MiB | Upload | Berhasil (dengan indikator progres terlihat) |
| R7 | File 12 MiB | Upload | Ditolak: `413 file_too_large`; aplikasi menampilkan "file terlalu besar (maks. 10 MB)" dan idealnya memeriksa ukuran sebelum mengirim |
| R8 | Tipe salah | Upload `.txt` yang diganti jadi `.jpg` | Ditolak dengan pesan jelas (server memeriksa isi file yang sebenarnya) |
| R9 | File kosong | Upload 0 byte | Ditolak dengan pesan jelas |
| R10 | Nama file panjang | Upload file bernama panjang | Diterima atau ditolak dengan rapi, tidak crash |
| R11 | Upload gagal di tengah | Pindah ke mode pesawat saat upload | Error dengan "coba lagi"; **pembelian tetap ada** dan bisa dicoba ulang dari layar detail |
| R12 | Batal di pemilih file | Buka pemilih file lalu keluar | Tidak terjadi apa-apa, tidak ada error |
| R13 | Izin ditolak | Tolak akses file/foto | Penjelasan tampil, aplikasi tidak crash |
| R14 | Tautan lama tidak dipakai ulang | Lihat struk, tunggu lebih dari 5 menit, tekan tautan atau layar lama | Aplikasi meminta tautan baru; tidak pernah menyimpan URL lama |
| R15 | Struk pengguna lain | Minta struk bob dengan token alice | `404` |
| R16 | File virus (hanya staging dengan `MALWARE_SCAN=on`) | Upload string uji EICAR yang disimpan sebagai file mirip PDF | Ditolak sebagai terinfeksi; file bersih tetap lolos |

### 3.5 Pengingat, preferensi, notifikasi push

Perlu HP asli dengan development/production build yang menyertakan `google-services.json`, dan backend dengan FCM yang sudah
dikonfigurasi (lihat `deploy/README.md` / `.env` dev).

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| N1 | Permintaan izin | Login pertama di Android 13+ | Ditanya sekali; aplikasi tetap berfungsi bila ditolak (pengingat tetap muncul di daftar dalam aplikasi) |
| N2 | Token terdaftar | Izinkan notifikasi | Server memiliki perangkatnya (`POST /devices` menjawab 200); mendaftar lagi setelah install ulang tidak membuat duplikat |
| N3 | Push uji dari Firebase | Firebase Console, "Send test message" dengan token | Notifikasi muncul (juga saat aplikasi tertutup) |
| N4 | Pengingat sungguhan | Buat pembelian yang garansinya berakhir besok; tunggu 1-2 menit | Push "Garansi segera berakhir ... besok" tiba; juga ada di daftar pengingat |
| N5 | Ketuk push | Ketuk notifikasinya | Aplikasi membuka pembelian yang sesuai (memakai `data.notificationId`) |
| N6 | Aplikasi di latar depan | Picu N4 saat aplikasi terbuka | Tampil sebagai banner dalam aplikasi atau di daftar, tidak crash |
| N7 | Tandai sudah dibaca | Buka pengingat, tandai dibaca | Lencana belum-dibaca berkurang; tetap terbaca setelah restart |
| N8 | Filter belum dibaca | Pakai tampilan hanya-belum-dibaca | Hanya yang belum dibaca |
| N9 | Simpan preferensi | Matikan pengingat 30 hari, simpan, buka lagi | Pengaturan tersimpan; saat menyimpan, `enabled`, `daysBefore` dan `channel` dikirim bersamaan |
| N10 | Matikan pengingat | Matikan pengingat, picu N4 | Tidak ada notifikasi untuk pembelian baru |
| N11 | Jam tenang | Atur jam tenang yang mencakup "sekarang", picu N4 | Notifikasi baru tiba setelah jam tenang berakhir |
| N12 | Ubah tanggal akhir | Setelah pengingat terjadwal, ubah tanggal akhir garansi | Pengingat lama tidak terkirim; yang baru sesuai tanggal baru |
| N13 | Hapus pembelian | Hapus sebelum waktu pengingat | Tidak ada pengingat yang tiba |
| N14 | Keluar menghentikan push | Keluar, lalu picu pengingat untuk pengguna itu | HP tidak menerima apa-apa |
| N15 | Dua pengguna di satu HP | Alice keluar, bob masuk di HP yang sama | Pengingat alice tidak pernah tampil untuk bob; token sekarang milik bob |
| N16 | Channel Android | Pengaturan sistem, notifikasi aplikasi | Ada channel bernama `reminders` dengan suara/tingkat kepentingan sesuai desain |
| N17 | Notifikasi diblokir di sistem | Matikan notifikasi untuk aplikasi | Aplikasi tetap menampilkan pengingat di dalamnya; tidak crash |

### 3.6 Akun: ekspor, hapus, batalkan penghapusan

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| C1 | Profil | Buka akun/pengaturan | Nama/e-mail tampil benar |
| C2 | Ekspor | Tekan ekspor data | JSON (atau CSV bila dikonversi) tersimpan/terbagikan; berisi pembelian, preferensi, perangkat; **bukan** file struknya |
| C3 | Hapus akun, login lama | Login, tunggu lebih dari 5 menit, minta penghapusan | Aplikasi mengarahkan pengguna login ulang (`reauthentication_required`), lalu percobaan ulang berhasil |
| C4 | Hapus akun, login baru | Login baru saja, minta penghapusan | `202`; aplikasi menampilkan tanggal penghapusan (7 hari lagi); e-mail konfirmasi masuk |
| C5 | Teks peringatan | Buka layar hapus | Jelas menyebut apa yang dihapus dan bahwa bisa dibatalkan sampai tanggal itu |
| C6 | Selama masa tenggang | Setelah C4, pakai aplikasi | Akun menampilkan "penghapusan dijadwalkan" dengan opsi batal; tidak ada pengingat yang dikirim |
| C7 | Batalkan penghapusan | Tekan batal | Penghapusan dibatalkan; e-mail mengonfirmasi; data utuh; pengingat berjalan lagi |
| C8 | E-mail pengingat | (Staging) hari ke-6 masa tenggang | E-mail pengingat tiba |
| C9 | Penghapusan terjadi | (Staging, pakai akun sekali pakai; atau minta tim backend memajukan tanggal) Setelah tanggalnya | Data, struk dan identitas login hilang; login lagi membuat akun baru yang kosong |
| C10 | Permintaan ganda | Minta penghapusan dua kali | Tidak crash; tanggal tidak bergeser |

### 3.7 Jaringan, error, keamanan

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| E1 | Mulai tanpa jaringan | Mode pesawat, buka aplikasi | Keadaan "tidak ada koneksi" yang jelas dengan tombol coba lagi; tidak ada spinner tanpa akhir |
| E2 | Koneksi putus di tengah aksi | Putus jaringan saat menyimpan pembelian | Error dan coba lagi, tidak ada pembelian ganda setelah diulang |
| E3 | Jaringan lambat | Batasi ke 2G/3G | Spinner tampil, tidak membeku, timeout menampilkan pesan |
| E4 | Server mati | Hentikan backend | Error ramah, bukan stack trace atau JSON mentah |
| E5 | Batas laju | Picu banyak permintaan cepat (sekitar 60 per menit) | `429` ditangani dengan menunggu dan mencoba lagi, bukan perulangan error |
| E6 | Error server 500 | (Tim backend mensimulasikan) | Pesan umum; `requestId` tersedia untuk dukungan |
| E7 | Token tidak ada di log | Periksa log perangkat (`adb logcat`) selama sesi | Tidak ada token atau password yang tercetak |
| E8 | Token disimpan aman | Periksa penyimpanan aplikasi | Token hanya di penyimpanan aman (Keystore), bukan preferensi biasa |
| E9 | Tangkapan layar/aplikasi terakhir | Buka pengalih aplikasi di layar struk | Sesuai keputusan desain (diburamkan atau tidak); minimal tidak crash |
| E10 | Hanya HTTPS (staging) | Periksa lalu lintas jaringan | Semua lewat HTTPS; tidak ada panggilan HTTP |
| E11 | Putar layar | Putar saat mengisi formulir dan saat upload | Data yang diketik tidak hilang; upload tidak terduplikasi |
| E12 | Aplikasi ke latar belakang | Kirim aplikasi ke latar belakang saat proses login, lalu kembali | Proses selesai atau mulai ulang dengan rapi |

### 3.8 Bahasa, aksesibilitas, perangkat

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| L1 | Indonesia dan Inggris | Ganti bahasa HP | Teks mengikuti bahasa (atau konsisten Indonesia sesuai desain); tidak ada label terpotong |
| L2 | Huruf besar | Skala font sistem ke maksimum | Layar tetap bisa dipakai, tombol terjangkau |
| L3 | Mode gelap | Ganti tema sistem | Sesuai desain atau setidaknya tetap terbaca |
| L4 | Pembaca layar | TalkBack aktif | Tombol dan kolom punya label yang bermakna |
| L5 | Layar kecil | HP 5 inci | Tidak ada elemen bertumpuk atau tombol tersembunyi |
| L6 | Tanggal dan uang | Periksa format | Tanggal mudah dibaca (mis. 21 Okt 2026), harga dalam Rupiah dengan pemisah ribuan |
| L7 | Tombol kembali | Pakai tombol kembali Android di setiap layar | Kembali dengan logis, tidak keluar di tengah formulir tanpa peringatan |

## 4. Perilaku server yang perlu diketahui saat menguji

* **Ubah (PATCH) menimpa semuanya.** Jika sebuah field hilang setelah diubah, berarti aplikasi lupa mengirimnya.
* **Filter status memakai tanggal server (UTC).** Selisih satu hari antara 00:00 dan 07:00 WIB adalah keterbatasan backend yang sudah diketahui.
* **Waktu pengingat:** pukul 09:00 di zona waktu pengguna (default WIB), pada 30/14/7/3/1 hari sebelum tanggal akhir. Pengingat dijadwalkan
  hingga 48 jam ke depan, dan yang terlewat tetap dikirim jika telat kurang dari 24 jam. Setiap pengingat hanya dikirim sekali.
* **Struk:** PDF, JPEG atau PNG sampai 10 MiB. Server memeriksa isi file yang sebenarnya, bukan ekstensinya.
* **Penghapusan akun** terjadi 7 hari setelah permintaan dan butuh login dalam 5 menit terakhir.
* Alasan di sisi server saat sebuah panggilan gagal ada di `docker compose logs web worker` (lokal) atau log server (staging).

## 5. Templat laporan bug

```
Judul:        singkat dan spesifik
Build / perangkat / versi Android:
Lingkungan:   lokal / staging
ID test case:
Langkah:      1. ... 2. ... 3. ...
Diharapkan:
Sebenarnya:
requestId:    (dari isi error, jika ada)
Lampiran:     tangkapan layar atau rekaman layar; potongan `adb logcat` tanpa token
Tingkat:      blocker (aplikasi tak bisa dipakai) / mayor (fitur rusak) / minor / kosmetik
```

## 6. Checklist rilis

- [ ] Smoke test lulus di HP asli
- [ ] Semua kasus di bagian 3.1 sampai 3.7 lulus atau punya pengecualian yang disetujui dan tercatat
- [ ] Pengingat push (N4) terverifikasi di perangkat asli terhadap staging
- [ ] Penghapusan akun dengan akun sekali pakai terverifikasi dari awal sampai akhir (C3, C4, C7, C9)
- [ ] Tidak ada token di log (E7), hanya HTTPS (E10)
- [ ] Teks privasi dan formulir toko aplikasi sesuai `06-data-and-privacy/data-retention-policy.md`
- [ ] Nomor versi dan catatan perubahan diperbarui
