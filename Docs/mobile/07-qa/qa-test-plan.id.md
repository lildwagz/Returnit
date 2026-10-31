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
| A13 | Kunci aplikasi: aktifkan | Pengaturan, nyalakan saklar "Kunci aplikasi" (HP punya kunci layar atau sidik jari) | Sistem meminta sidik jari, wajah, atau PIN; setelah berhasil saklar tetap menyala |
| A14 | Kunci aplikasi: HP tanpa kunci layar | Hapus kunci layar HP, lalu coba menyalakannya | Pesan agar mengatur kunci layar dulu; saklar tetap mati |
| A15 | Kunci aplikasi saat dibuka | Dengan kunci aktif, tutup paksa aplikasi lalu buka | Layar "ReturnIt terkunci" dengan permintaan sistem; isi aplikasi tidak terlihat sebelum dibuka |
| A16 | Kunci aplikasi setelah ditinggalkan | Tinggalkan aplikasi lebih dari 30 detik, lalu kembali | Terkunci lagi, dan setelah dibuka kamu kembali ke layar yang sama |
| A17 | Kunci aplikasi, absen singkat | Tinggalkan aplikasi kurang dari 30 detik (membalas pesan), lalu kembali | Tidak terkunci |
| A18 | Kunci aplikasi: batal | Di layar kunci, batalkan permintaan sistem | Tetap terkunci; "Buka kunci" mencoba lagi; layar di bawahnya tidak bisa disentuh |
| A19 | Kunci aplikasi: matikan | Matikan saklar di Pengaturan | Meminta sidik jari, wajah, atau PIN dulu; setelahnya aplikasi terbuka tanpa permintaan |
| A20 | Kunci aplikasi: keluar dari layar kunci | Ketuk "Keluar dari akun" di layar kunci | Kembali ke layar masuk; tidak ada data akun yang terlihat |
| A21 | Kunci aplikasi dan dialog sistem | Setelah dibuka, munculkan dialog sistem lagi dengan mematikan kunci | Membuka dan menutup dialog sistem tidak pernah mengunci aplikasi dengan sendirinya |

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
| N18 | Ganti channel setelah pengingat tiba | Terima pengingat (kotak masuk), buka sampai berstatus dibaca. Di pengaturan pengingat ganti channel push <-> in_app (dan kembali), tunggu 2 menit, segarkan kotak masuk | Tetap SATU entri untuk pengingat itu, tetap berstatus dibaca. Tidak muncul salinan baru. Pengingat yang belum jatuh tempo dikirim sekali, lewat channel yang dipilih saat jatuh tempo |

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
| E7 | Token tidak ada di log | Periksa log perangkat (`adb logcat`) selama sesi | Tidak ada token atau password yang tercetak. Baris berawalan `[returnit]` adalah log permintaan milik app sendiri (metode, path, status, waktu) dan memang ada (lihat V1 sampai V3) |
| E8 | Token disimpan aman | Periksa penyimpanan aplikasi | Token hanya di penyimpanan aman (Keystore), bukan preferensi biasa |
| E9 | Tangkapan layar/aplikasi terakhir | Buka pengalih aplikasi di layar struk | Sesuai keputusan desain (diburamkan atau tidak); minimal tidak crash |
| E10 | Hanya HTTPS (staging) | Periksa lalu lintas jaringan | Semua lewat HTTPS; tidak ada panggilan HTTP |
| E11 | Putar layar | Putar saat mengisi formulir dan saat upload | Data yang diketik tidak hilang; upload tidak terduplikasi |
| E12 | Aplikasi ke latar belakang | Kirim aplikasi ke latar belakang saat proses login, lalu kembali | Proses selesai atau mulai ulang dengan rapi |
| E13 | Pembaruan wajib (saat dibuka) | Tim backend menaikkan versi minimum di atas build kamu (`./publish-apk.sh min <versi>`), lalu buka aplikasi | Layar penuh "Perbarui aplikasi" dengan versimu dan versi minimum; tombol "Perbarui sekarang" dan "Saya sudah memperbarui"; tidak ada layar lain yang bisa dibuka |
| E14 | Pembaruan wajib (aplikasi sudah terbuka) | Dengan aplikasi terbuka di Home, naikkan versi minimum, lalu tarik untuk menyegarkan | Layar kunci yang sama muncul setelah panggilan API itu (`426`); tidak ada yang berfungsi sampai diperbarui |
| E15 | Ajakan memperbarui | Setel versi terbaru di atas build kamu dan versi minimum di bawahnya | Banner "Versi X tersedia" di Home; "Nanti" menyembunyikannya sampai aplikasi ditutup dan tidak muncul lagi untuk versi yang sama; aplikasi tetap bisa dipakai |
| E16 | Pemeliharaan | `./publish-apk.sh maintenance on "pesan"` | Layar "Sedang dalam perbaikan" dengan pesan dan "Coba lagi"; setelah `maintenance off`, "Coba lagi" kembali normal |
| E17 | Offline tidak pernah mengunci | Mode pesawat, lalu buka aplikasi | Aplikasi terbuka normal (keadaan offline), tanpa layar pembaruan atau pemeliharaan |
| E18 | Unduh dari layar kunci | Ketuk "Perbarui sekarang" | Browser membuka `https://dl.<domain>/`, APK terunduh, dan SHA-256-nya sama dengan yang ada di halaman |

### 3.8 Bahasa, aksesibilitas, perangkat

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| L1 | Indonesia, Inggris, Arab | Ganti bahasa di app (lihat LN1 sampai LN10) dan bahasa HP | Teks mengikuti bahasa yang dipilih; tidak ada label terpotong |
| L2 | Huruf besar | Skala font sistem ke maksimum | Layar tetap bisa dipakai, tombol terjangkau |
| L3 | Mode gelap | Ganti tema sistem | Sesuai desain atau setidaknya tetap terbaca |
| L4 | Pembaca layar | TalkBack aktif | Tombol dan kolom punya label yang bermakna |
| L5 | Layar kecil | HP 5 inci | Tidak ada elemen bertumpuk atau tombol tersembunyi |
| L6 | Tanggal dan uang | Periksa format | Tanggal mudah dibaca (mis. 21 Okt 2026), harga dalam Rupiah dengan pemisah ribuan |
| L7 | Tombol kembali | Pakai tombol kembali Android di setiap layar | Kembali dengan logis, tidak keluar di tengah formulir tanpa peringatan |

### 3.9 Fitur yang ditambahkan di versi 1.3.x

Jalankan di build 1.3.2 atau lebih baru, terhadap backend dengan migrasi 11 atau lebih baru.

**Batas pengembalian**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| T1 | Tambah dengan masa pengembalian | Di formulir pilih "14 hari" | Detail menampilkan banner "Batas pengembalian" dengan tanggalnya (tanggal beli + 14 hari) dan sisa harinya |
| T2 | Pilihan | Buka formulir | "Tidak ada", 7, 14, dan 30 hari; "Tetap" hanya muncul saat mengedit catatan yang tanggalnya tidak cocok dengan pilihan mana pun |
| T3 | Pengingat pengembalian | Atur batas 3 hari lagi dan tunggu proses pukul 09.00 (atau minta tim backend memajukan jam) | Pengingat tentang **batas pengembalian** (bukan garansi) datang 3 hari dan 1 hari sebelumnya |
| T4 | Dua jenis sekaligus | Pembelian dengan akhir garansi dan batas pengembalian yang berdekatan | Kedua pengingat datang; tidak saling menggantikan |
| T5 | Batas tanpa garansi | Masa pengembalian diisi, garansi "Tidak diketahui" | Pengingat pengembalian tetap datang; garansi tampil tidak diketahui |
| T6 | Ubah atau hapus | Ubah batasnya, lalu hapus | Pengingat lama tidak terkirim; tanggal baru dipakai; tidak ada pengingat setelah dihapus |
| T7 | Kartu di daftar | Batas dalam 14 hari | Kartu menampilkan "Batas pengembalian: N hari lagi" (kuning bila 3 hari atau kurang) dan "hari ini" pada harinya; tidak tampil bila masih jauh atau sudah lewat |
| T8 | Validasi | API: `returnDeadline` sebelum tanggal beli | `400 validation_error` |

**Ringkasan di Home**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| H1 | Penghitung | Buka Home dan bandingkan dengan filter di daftar | Aktif, segera berakhir, dan total sama (datanya dari `GET /summary`) |
| H2 | Nilai yang masih bergaransi | Dua pembelian aktif dengan harga (satu mata uang lain) | Jumlah tampil per mata uang, dipisah "·"; pembelian kedaluwarsa tidak dihitung |
| H3 | Tanggal berikutnya | Lihat "Garansi berakhir berikutnya" dan "Batas pengembalian terdekat" | Pembelian dan tanggal yang tepat; diketuk membuka pembeliannya |
| H4 | Akun kosong | Akun baru | Tidak ada kartu sorotan, keadaan kosong seperti biasa |

**Ingatkan lagi (snooze)**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| Z1 | Hanya pilihan yang masuk akal | Pengingat terkirim yang tanggalnya besok / 3 hari lagi / 7+ hari lagi / hari ini atau lewat | "Ingatkan lagi" menawarkan: Besok / Besok dan 3 hari lagi / ketiganya / **tanpa tombol** |
| Z2 | Tunda | Pilih "Besok pukul 09.00" | Toast dengan tanggal dan jam; yang asli ditandai dibaca; salinannya datang besok pukul 09.00 (setelah jam tenang bila berlaku) |
| Z3 | Tunda lagi | Tunda pengingat yang sama saat salinannya masih menunggu | Waktunya bergeser; tidak ada duplikat |
| Z4 | Tanggal berubah | Tunda, lalu ubah tanggal akhir | Salinan yang ditunda tidak dikirim |
| Z5 | Ditolak server | Postman: pilihan yang jatuh setelah tanggal akhir | `422 snooze_not_possible`; app menampilkan pesan jelas bila ini terjadi |
| Z6 | Pengingat orang lain | Id notifikasi pengguna lain | `404` |

**Kalender**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| K1 | Akhir garansi | Detail, "Tambahkan akhir garansi ke kalender" | Layar acara baru bawaan HP terbuka dengan acara seharian di tanggal itu; disimpan menambahkannya |
| K2 | Batas pengembalian | Sama untuk batas pengembalian | Sama, dengan judul pengembalian |
| K3 | Tanpa izin | Periksa izin aplikasi | Tidak ada izin kalender yang diminta atau tercantum |
| K4 | Batal | Keluar dari layar kalender | Tidak ada yang tersimpan, tidak ada error |

**Bahasa (Indonesia, Inggris, Arab)**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| LN1 | Bawaan | HP diatur ke Indonesia, Inggris, Arab, lalu bahasa lain | App mengikuti Indonesia, Inggris, Arab; bahasa lain menjadi Inggris |
| LN2 | Pilih bahasa | Pengaturan, "Bahasa", pilih satu per satu | Tab, daftar, formulir, peringatan, error, nama bulan berubah; angka memakai "." sebagai pemisah ribuan di Indonesia dan "," di Inggris dan Arab |
| LN3 | Tersimpan | Tutup dan buka lagi | Pilihan tetap; "Ikuti bahasa HP" kembali ke bahasa HP |
| LN4 | Dari layar login | Sebelum masuk, ketuk "Bahasa" | Berfungsi; halaman masuk terbuka dalam bahasa yang sama |
| LN5 | Indonesia dan Inggris | Pindah di antara keduanya | Berubah seketika, tanpa memulai ulang |
| LN6 | Arab | Pilih Arab | Pemberitahuan meminta app dibuka ulang; sesudahnya tata letak kanan ke kiri, panah dicerminkan, font Arab, angka Barat |
| LN7 | Kembali ke Inggris | Setelah LN6 pilih Inggris, tutup dan buka lagi | Tata letak kembali kiri ke kanan (diperbaiki di 1.3.1) |
| LN8 | Bentuk jamak | Periksa "sisa hari" untuk 1, 2, 3, 11 hari dalam Arab, dan 1 lawan 2 dalam Inggris | Bentuk yang benar setiap kali |
| LN9 | Error dan formulir | Picu validasi dan error server | Pesan dalam bahasa yang dipilih |
| LN10 | Celah yang diketahui | Teks pengingat dan e-mail dari server | Masih bahasa Indonesia (bahasa di backend direncanakan) |
| LN11 | Redaksi Arab | Minta penutur asli membaca app dan halaman masuk | Ditulis tanpa peninjau penutur asli: catat koreksinya |

**Tab Pengingat dan pengaturannya**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| M1 | Tata letak | Buka tab Pengingat | Judul dengan ikon gir, filter "Semua / Belum dibaca", lalu riwayat; tanpa formulir pengaturan, tanpa daftar garansi |
| M2 | Gir | Ketuk gir | Layar pengaturan pengingat terbuka (sama dengan Pengaturan, "Preferensi pengingat") |
| M3 | Status di Pengaturan | Lihat baris "Preferensi pengingat" | Menampilkan mis. "Aktif · 7, 1 hari sebelumnya · Notifikasi HP" atau "Mati"; berubah setelah disimpan |
| M4 | Lencana belum dibaca | Punya 2 pengingat belum dibaca | Lencana "2" di tab; membuka satu menurunkannya; hilang di 0 ("50+" di batas) |
| M5 | Filter | "Belum dibaca" | Hanya yang belum dibaca |
| M6 | Peringatan notifikasi | Kanal Notifikasi HP, notifikasi diblokir di pengaturan sistem | Pemberitahuan di atas dengan "Izinkan notifikasi" atau "Buka pengaturan aplikasi"; tidak tampil bila diizinkan, kanalnya e-mail atau dalam aplikasi, atau pengingat dimatikan |
| M7 | Kosong saat mati | Matikan pengingat | "Pengingat dimatikan" dengan tombol "Aktifkan pengingat" ke pengaturan |
| M8 | Simpan pengaturan | Ubah hari, kanal, dan jam tenang, simpan, buka lagi | Tersimpan; ada pesan bila tidak ada hari yang dipilih atau jam bukan JJ:MM |

**Nama dan sapaan**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| U1 | Sapaan | Masuk | Home menyapa "Halo, <nama depan> 👋"; tanpa nama, sapaan umum |
| U2 | Nama mengikuti login | Ubah nama di server login, masuk lagi | Akun dan sapaan menampilkan nama baru |
| U3 | Google | Daftar dengan akun Google | Nama sesuai profil Google |

**Halaman masuk (tema Keycloak)**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| KC1 | Tampilan | Buka halaman masuk, daftar, dan lupa kata sandi | Logo ReturnIt, warna merek, kartu membulat; mode gelap mengikuti HP |
| KC2 | Bahasa | Pilih tiap bahasa di app, lalu masuk | Halaman terbuka dalam bahasa itu; Arab kanan ke kiri |
| KC3 | Daftar | Cari cara mendaftar | Tautan "Belum punya akun? Daftar" di halaman masuk (app tidak punya tombol daftar) |
| KC4 | Pesan | Kata sandi salah, e-mail yang sudah ada | Pesan dalam bahasa yang dipilih |

**Log aplikasi**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| V1 | Permintaan tercatat | `adb logcat` disaring `returnit` saat memakai app | Satu baris per permintaan: metode, path, status, waktu dalam ms; peristiwa masuk dan keluar |
| V2 | Rahasia tidak pernah tercatat | Baca barisnya | Tidak ada token akses, kata sandi, token push, atau tautan struk; tidak ada isi permintaan atau respons di build rilis |
| V3 | Isi (hanya build debug) | Build dengan `EXPO_PUBLIC_LOG_BODY=true` atau build pengembangan | Isi JSON muncul dengan rahasia diganti `[hidden]`, dipotong di 2000 karakter |

**Halaman unduhan**

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| W1 | Halaman | Buka `https://dl.<domain>/` di HP dan komputer | Halaman berbahasa Inggris; versi, ukuran, dan tanggal sama dengan `latest.json`; "What's new", fitur, langkah instal, FAQ; terang dan gelap |
| W2 | Berkas | Ketuk "Download APK" | APK terunduh; SHA-256 sama dengan yang di halaman; dipasang di atas versi lama dan data tetap |
| W3 | Aset | Lihat halaman | Logo dan font termuat (tidak ada gambar rusak, tidak ada kilatan font lain) |
| W4 | Setelah rilis | Jalankan `./publish-apk.sh publish ...` | Halaman langsung menampilkan versi dan catatan baru |

### 3.10 Kategori (versi 1.5.0)

Jalankan di build 1.5.0 atau lebih baru, dengan backend yang sudah memakai migrasi 12 atau lebih baru.

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| C1 | Pilih saat menambah | Buka formulir tambah; baris ikon ada di bawah nama produk; pilih "Ponsel & tablet" | Chip terisi; setelah disimpan kartu menampilkan ikon ponsel dan layar detail menampilkan "Kategori" |
| C2 | Opsional | Simpan pembelian tanpa memilih | Tersimpan; kartu tanpa kotak ikon; detail tanpa kategori |
| C3 | Hapus dengan mengetuk lagi | Ketuk chip yang dipilih sekali lagi | Chip tidak terisi lagi; saat disimpan tidak ada kategori yang dikirim |
| C4 | Saran garansi | Pembelian baru, jangan sentuh durasi, pilih "Peralatan rumah" | Durasi berubah ke 24 bulan dengan teks "Durasi garansi disetel ke 24 bulan..." |
| C5 | Pilihan sendiri menang | Pilih 6 bulan dulu, lalu pilih kategori | Durasi tetap 6 bulan, tanpa teks saran |
| C6 | Tanpa saran | Pilih "Kendaraan" atau "Lainnya" | Durasi tetap seperti sebelumnya |
| C7 | Edit mempertahankan | Edit pembelian yang punya kategori, ubah nama saja | Kategori tidak berubah |
| C8 | Edit menghapus | Edit, ketuk chip yang dipilih untuk menghapus, simpan | Kartu dan detail tanpa kategori |
| C9 | Filter | Di Pembelian ketuk "Kategori", pilih "Dapur" | Hanya pembelian dapur; tombol terisi dengan ikon dan nama; baris jumlah sesuai |
| C10 | Filter dengan status dan pencarian | Biarkan "Dapur", ketuk "Aktif", lalu ketik nama | Hasil memenuhi ketiganya |
| C11 | Hapus filter | Ketuk x di samping tombol atau "Semua kategori" di sheet | Daftar penuh kembali |
| C12 | Pembelian lama | Buka pembelian yang dibuat sebelum 1.5.0 | Tampil tanpa kategori, dan bisa dibuka dan diedit seperti biasa |
| C13 | Aplikasi lama di server baru | Dengan 1.4.1 (bila masih diizinkan), edit pembelian yang punya kategori | Kategori tetap (server mempertahankan field yang tidak dikirim) |
| C14 | Validasi API | `POST /purchases` dengan `"category":"bogus"`; `GET /purchases?category=bogus` | Keduanya `400 validation_error` |
| C15 | Bahasa dan tata letak | Arab, font sistem besar, lebar 375 px | Baris ikon bergeser ke samping dan mulai dari sisi awal (kanan di Arab); nama chip tidak terpotong; baris sheet terbaca |
| C16 | Pembaca layar | TalkBack di baris ikon, tombol filter, dan kartu | Chip membaca nama dan status terpilih; kartu membaca kategori lebih dulu; ikon tidak terbaca dua kali |

### 3.12 Tab Tenggat (versi 1.6.0)

Jalankan di build 1.6.0 atau lebih baru, dengan backend yang sudah memiliki `GET /deadlines`.

| ID | Kasus | Langkah | Hasil yang diharapkan |
|---|---|---|---|
| D1 | Tab | Buka aplikasi setelah masuk | Lima tab: Beranda, Pembelian, Tenggat, Pengingat, Pengaturan; ikon Tenggat berupa kalender |
| D2 | Kelompok | Dengan pembelian yang berakhir 2 hari lagi, 20 hari lagi, dan 3 bulan lagi, serta satu yang berakhir 5 hari lalu | Bagian "Minggu ini", "Bulan ini", "Nanti", dan "Sudah lewat" berurutan, masing-masing hanya bila ada isinya; tiap baris ada di bagian yang benar |
| D3 | Satu baris | Lihat satu baris | Nama produk, "Akhir garansi" atau "Batas pengembalian", tanggal, dan hitung mundur ("3 hari lagi", "hari ini", "5 hari lalu") berwarna seperti kartu |
| D4 | Dua tanggal | Pembelian dengan akhir garansi dan batas pengembalian | Dua baris, satu untuk tiap jenis |
| D5 | Buka | Ketuk satu baris | Detail pembelian terbuka; Kembali membawa ke Tenggat di posisi yang sama |
| D6 | Kosong | Akun tanpa tanggal | Keadaan kosong ("Belum ada tenggat"), tanpa error |
| D7 | Segarkan | Tambah pembelian bergaransi, kembali ke tab; tarik ke bawah | Baris baru muncul; tarik untuk menyegarkan berfungsi |
| D8 | Yang lama | Garansi yang berakhir 40 hari lalu | Tidak tampil (daftar hanya mundur 30 hari) |
| D9 | Kategori | Pembelian yang punya kategori | Baris menampilkan ikon kategori |
| D10 | Offline dan kesalahan | Buka tanpa koneksi | Pesan kesalahan dengan "Coba lagi"; tidak ada yang crash |
| D11 | Bahasa dan tata letak | Arab, font sistem besar, lebar 375 px | Baris mulai dari sisi awal, teks terbungkus atau terpotong dengan rapi, label tab muat |
| D12 | Pembaca layar | TalkBack di satu baris dan di judul bagian | Baris dibaca produk, jenis, tanggal, dan hitung mundur; judul bagian diumumkan sebagai judul |

## 4. Perilaku server yang perlu diketahui saat menguji

* **Ubah (PATCH) menimpa semuanya.** Jika sebuah field hilang setelah diubah, berarti aplikasi lupa mengirimnya.
* **Filter status memakai tanggal server (UTC).** Selisih satu hari antara 00:00 dan 07:00 WIB adalah keterbatasan backend yang sudah diketahui.
* **Waktu pengingat:** pukul 09:00 di zona waktu pengguna (default WIB), pada 30/14/7/3/1 hari sebelum tanggal akhir. Pengingat dijadwalkan
  hingga 48 jam ke depan, dan yang terlewat tetap dikirim jika telat kurang dari 24 jam. Setiap pengingat hanya dikirim sekali.
* **Struk:** PDF, JPEG atau PNG sampai 10 MiB. Server memeriksa isi file yang sebenarnya, bukan ekstensinya.
* **Batas pengembalian:** pengingat 3 hari dan 1 hari sebelum batas, pukul 09:00 di zona waktu pengguna (tidak mengikuti "hari sebelumnya" yang dipilih untuk garansi). Pengingat garansi dan pengingat pengembalian terpisah.
* **Snooze:** pilihannya besok, 3 hari lagi, dan minggu depan pukul 09:00 (setelah jam tenang bila berlaku); pilihan yang jatuh setelah tanggal yang dibahas pengingat tidak ditawarkan, dan server menjawab 422 bila tetap diminta.
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
- [ ] Semua kasus di bagian 3.1 sampai 3.12 lulus atau punya pengecualian yang disetujui dan tercatat
- [ ] Pengingat push (N4) terverifikasi di perangkat asli terhadap staging
- [ ] Penghapusan akun dengan akun sekali pakai terverifikasi dari awal sampai akhir (C3, C4, C7, C9)
- [ ] Tidak ada token di log (E7), hanya HTTPS (E10)
- [ ] Teks privasi dan formulir toko aplikasi sesuai `06-data-and-privacy/data-retention-policy.md`
- [ ] Teks Arab (app dan halaman masuk) sudah dibaca penutur asli (LN11)
- [ ] Dari Arab kembali ke Inggris, tata letak kembali kiri ke kanan (LN7)
- [ ] Tidak ada isi permintaan atau respons di build yang dipublikasikan (V2)
- [ ] Nomor versi dan catatan perubahan diperbarui, dan halaman unduhan menampilkan versi baru (W1)
