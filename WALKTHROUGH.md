# Evo-Sched Walkthrough

Bu dosyanın amacı projede nereye gitmek istediğimizi, şu ana kadar ne yaptığımızı ve hangi aşamada olduğumuzu geriye dönük olarak takip edilebilir hale getirmektir.

## 1. Amaç ve Hedeflenen Yol

Projenin ana hedefi, Kubernetes içinde çalışan kendi scheduler'ımızı geliştirmek ve daha sonra bu scheduler'ın karar verme politikasını evolutionary optimization ile iyileştirmektir.

Proje iki ana katmandan oluşur:

1. Gerçek Kubernetes üzerinde çalışan custom scheduler
2. Bu scheduler'ın scoring/decision parametrelerini optimize eden evolutionary algorithm

İlk aşamadaki teknik hedef:

- `schedulerName: evok8s-scheduler` verilen pod'ları default scheduler yerine bizim scheduler'ın ele alması
- Uygun node'ları görmesi
- Basit bir strateji ile node seçmesi
- Pod'u seçilen node'a bind etmesi

Bu ilk versiyonda amaç "önce çalışan sistem" yaklaşımıdır. Zeka ve optimizasyon daha sonra eklenecektir.

Planlanan yol kabaca şöyledir:

1. Local Kubernetes ortamını kurmak
2. Basit bir custom scheduler yazmak
3. Scheduler'ı cluster içine deployment olarak koymak
4. Gerçek pod'lar üzerinde scheduler davranışını test etmek
5. Karar mekanizmasını parametreli hale getirmek
6. Tekrar üretilebilir workload/trace tabanı kurmak
7. Fitness tanımlamak
8. Evolutionary algorithm ile policy parametrelerini optimize etmek
9. Baseline scheduler'larla karşılaştırmak
10. Sonuçları raporlamak

## 2. Bu Session'dan Önceki Durum

Bu session başlamadan önce repo ve cluster tarafında şu durum vardı:

### Hazır olanlar

- `kind` tabanlı local Kubernetes cluster kurulmuştu
- Cluster topolojisi [kind-config.yaml](./kind-config.yaml) içinde tanımlıydı
- Cluster çalışıyordu ve context `kind-evo-sched` idi
- `evo-sched` namespace mevcuttu
- Scheduler kodu [scheduler/main.go](./scheduler/main.go) içinde yazılmıştı
- Scheduler, `schedulerName == evok8s-scheduler` olan ve henüz schedule edilmemiş pod'ları tarayacak şekilde çalışıyordu
- Node seçimi için basit `least-pod-count` benzeri bir strateji vardı
- Pod binding mantığı kod içinde mevcuttu
- Docker image build edilmişti: `evok8s-scheduler:0.1.0`
- RBAC manifesti [rbac.yaml](./rbac.yaml) içinde mevcuttu
- ServiceAccount / ClusterRole / ClusterRoleBinding cluster'a uygulanmış durumdaydı

### Eksik olanlar

- Scheduler'ı cluster içinde çalıştıran bir deployment manifesti yoktu
- Repo kökünde `deploy.yaml` bulunmuyordu
- `evo-sched` namespace içinde çalışan custom scheduler pod'u yoktu
- `schedulerName: evok8s-scheduler` kullanan test workload manifesti yoktu
- Bu nedenle custom scheduler'ın gerçekten bind yaptığı henüz uçtan uca doğrulanmamıştı

Özetle, custom scheduler kodu ve gerekli yetkiler hazırlanmıştı; fakat scheduler henüz cluster içine deploy edilmemişti ve gerçek workload üzerinde test edilmemişti.

## 3. Bu Session'da Yaptıklarımız

Bu session'da amaç, 1. katmanda "çalışan gerçek scheduler" noktasına ulaşmaktı.

### 3.1. Mevcut durumu doğruladık

Önce repo ve cluster'ı kontrol ettik:

- Cluster'ın çalıştığını doğruladık
- Node'ların `Ready` durumda olduğunu gördük
- `evo-sched` namespace'in var olduğunu doğruladık
- RBAC kaynaklarının cluster'da bulunduğunu gördük
- Buna karşılık `evo-sched` namespace içinde çalışan scheduler pod'u olmadığını tespit ettik

Bu kontrol bize projenin tam olarak şu noktada olduğunu gösterdi:

- Cluster hazır
- Scheduler kodu hazır
- RBAC hazır
- Deployment ve gerçek test eksik

### 3.2. Scheduler deployment manifestini oluşturduk

Repo köküne [deploy.yaml](./deploy.yaml) dosyası eklendi.

Bu manifest ile:

- `evok8s-scheduler` isimli bir `Deployment` tanımlandı
- Namespace olarak `evo-sched` kullanıldı
- `serviceAccountName: evok8s-scheduler` ayarlandı
- Image olarak `evok8s-scheduler:0.1.0` kullanıldı
- `SCHEDULER_NAME=evok8s-scheduler` env variable'ı verildi

Bu adım, custom scheduler binary'sini cluster içinde çalışan gerçek bir pod haline getirdi.

### 3.3. Image'ı kind cluster node'larına yükledik

Local Docker image'ın kind node'ları tarafından kullanılabilmesi için image cluster'a yüklendi.

Uygulanan adım:

- `evok8s-scheduler:0.1.0` image'ı `evo-sched-control-plane`, `evo-sched-worker` ve `evo-sched-worker2` node'larına taşındı

Bu adım gerekliydi; aksi halde deployment ayağa kalksa bile image çekme hatası alınabilirdi.

### 3.4. Manifestleri cluster'a uyguladık

Şu kaynaklar apply edildi:

- [rbac.yaml](./rbac.yaml)
- [deploy.yaml](./deploy.yaml)

Sonuç:

- RBAC kaynakları `unchanged` olarak doğrulandı
- `evok8s-scheduler` deployment'ı cluster içinde oluşturuldu

### 3.5. Scheduler pod'unun çalıştığını doğruladık

Deployment sonrası scheduler pod'unun `Running` durumda olduğu görüldü.

Ayrıca loglarda şu başlangıç mesajı doğrulandı:

- `scheduler started name=evok8s-scheduler interval=2s`

Bu, scheduler process'inin cluster içinde başarıyla başladığını gösterdi.

### 3.6. Test workload manifestini oluşturduk

Repo köküne [test-workload.yaml](./test-workload.yaml) dosyası eklendi.

Bu manifest ile:

- `scheduler-smoke` isimli bir `Deployment` oluşturuldu
- `replicas: 3` kullanıldı
- Pod template içinde `schedulerName: evok8s-scheduler` ayarlandı
- Böylece bu pod'ların default scheduler yerine custom scheduler tarafından ele alınması sağlandı

### 3.7. Uçtan uca smoke test yaptık

Test workload cluster'a apply edildi ve ardından şu sonuçlar doğrulandı:

- Test pod'ları `Running` durumuna geçti
- Pod'lar worker node'lara yerleştirildi
- Scheduler loglarında bind mesajları görüldü

Bu nokta çok önemlidir; çünkü ilk kez şu ifade teknik olarak doğrulanmış oldu:

"Kubernetes içinde çalışan custom scheduler'ımız, kendisine yönlendirilen pod'ları gerçek node'lara bind ediyor."

### 3.8. Node filtreleme ekledik ve gerçek cluster üzerinde doğruladık

İlk smoke testten sonra scheduler'ın davranışını Kubernetes scheduling mantığına biraz daha yaklaştırmak için scoring öncesi node filtreleme eklendi.

Kod tarafında şu filtreler eklendi:

- Node `Ready=True` değilse elenir
- Node `spec.unschedulable=true` ise elenir
- Node üzerindeki `NoSchedule` ve `NoExecute` taint'leri pod tarafından tolere edilmiyorsa elenir

Bu değişiklik [scheduler/main.go](./scheduler/main.go) içine eklendi.

Ayrıca davranışı doğrulamak için [scheduler/main_test.go](./scheduler/main_test.go) dosyasında unit testler yazıldı.

Bu testler şu mantıkları doğrular:

- Hazır olmayan node elenir
- Cordoned node elenir
- Toleration olmayan taint'li node elenir
- Uygun toleration varsa node tekrar aday olabilir

`go test ./...` başarılı şekilde geçti.

### 3.9. Güncellenmiş scheduler image'ını yeniden deploy ettik

Node filtreleme eklendikten sonra:

- Scheduler image'ı yeniden build edildi
- Yeni image kind cluster node'larına tekrar yüklendi
- `evok8s-scheduler` deployment'ı rollout restart ile yeniden başlatıldı

Bu sayede cluster içinde çalışan scheduler pod'u yeni filtreleme mantığını kullanmaya başladı.

### 3.10. `cordon` testi yaptık

İlk doğrulama için `evo-sched-worker2` node'u cordon edildi.

Beklenti:

- Scheduler bu node'u artık aday olarak görmemeliydi

Ardından test workload yeniden oluşturuldu ve sonuç şu oldu:

- Tüm yeni pod'lar `evo-sched-worker` üzerine yerleşti
- `evo-sched-worker2` seçilmedi

Bu test, `spec.unschedulable` filtresinin doğru çalıştığını doğruladı.

### 3.11. Taint / toleration testi yaptık

İkinci doğrulama için `evo-sched-worker2` node'una şu taint eklendi:

- `dedicated=test:NoSchedule`

Önce toleration'sız workload yeniden çalıştırıldı.

Sonuç:

- Pod'lar `evo-sched-worker2` yerine yine `evo-sched-worker` üzerine yerleşti

Sonra bu taint'i tolere eden ayrı bir workload tanımlandı:

- [test-workload-toleration.yaml](./test-workload-toleration.yaml)

Bu workload içinde şu toleration vardı:

- `key=dedicated`
- `value=test`
- `effect=NoSchedule`

Sonuç:

- Yeni pod'ların bir kısmı `evo-sched-worker2` üzerine yerleşti
- Scheduler log'larında `worker2` için bind kararları görüldü

Bu test, taint filtresinin ve toleration eşleşmesinin doğru çalıştığını doğruladı.

### 3.12. Test sonrası cluster'ı temizledik

Doğrulama adımlarından sonra test ortamını sade hale getirmek için:

- Test deployment'ları silindi
- `evo-sched-worker2` üzerindeki test taint'i kaldırıldı

Böylece cluster bir sonraki geliştirme adımı için temiz bırakıldı.

### 3.13. Sabit heuristikten parametreli scoring modeline geçtik

Node filtreleme tamamlandıktan sonra scheduler'ın karar verme kısmı sabit `least-pod-count` mantığından çıkarıldı ve ağırlıklı bir scoring modeline taşındı.

Bu değişiklikle birlikte [scheduler/main.go](./scheduler/main.go) içinde şu yapılar eklendi:

- `ResourceRequest`
- `NodeState`
- `ScoreWeights`

Yeni akış şu şekilde çalışır:

1. Tüm node'lar için allocatable CPU/RAM bilgisi çıkarılır
2. O anda node üzerinde bulunan pod'ların toplam requested CPU/RAM değerleri toplanır
3. Her node için `FreeCPU`, `FreeMem` ve `PodCount` türetilir
4. Filter'dan geçen aday node'lar için skor hesaplanır
5. En yüksek skorlu node seçilir

Scoring formülü ilk versiyonda şu bileşenleri kullanır:

- `normFreeCPU`
- `normFreeMem`
- `normPodCount`
- `normPacking`

Skor, env ile verilen ağırlıklarla hesaplanır:

- `WEIGHT_FREE_CPU`
- `WEIGHT_FREE_MEM`
- `WEIGHT_POD_PENALTY`
- `WEIGHT_PACKING`

Bu değişiklik scheduler'ı evolutionary optimization için uygun hale getiren en kritik refactor adımıdır; çünkü artık davranış kod içine gömülü bir kural değil, dışarıdan ayarlanabilen bir policy yüzeyidir.

### 3.14. Resource fit kontrolü ekledik

Scoring refactor'u sırasında sadece puanlama eklenmedi; aynı zamanda pod request'inin node'a sığıp sığmadığı da kontrol edilmeye başlandı.

Bu sayede:

- CPU veya memory açısından yetmeyen node'lar artık seçilmiyor
- `packing` skoru yalnızca gerçekten pod'u alabilecek node'lar arasında değerlendiriliyor

Bu, parametrik scoring'in yanlış problem üzerinde çalışmasını engelleyen önemli bir düzeltmedir.

### 3.15. Scoring davranışı için unit testler ekledik

[scheduler/main_test.go](./scheduler/main_test.go) dosyası genişletildi.

Unit testlerde şu davranışlar doğrulandı:

- Pod container request'leri doğru toplanıyor mu
- Weight'ler env'den doğru okunuyor mu
- `FreeCPU` baskınsa daha boş node seçiliyor mu
- `PodPenalty` baskınsa daha az pod'lu node seçiliyor mu
- `Packing` baskınsa daha dolu ama sığan node seçiliyor mu
- Hiçbir node pod'u sığdıramıyorsa seçim boş dönüyor mu

`go test ./...` başarılı şekilde geçti.

### 3.16. Deployment manifestine scoring weight env'leri ekledik

[deploy.yaml](./deploy.yaml) güncellendi.

Deployment içine varsayılan scoring weight'leri env olarak eklendi:

- `WEIGHT_FREE_CPU=1.0`
- `WEIGHT_FREE_MEM=1.0`
- `WEIGHT_POD_PENALTY=0.5`
- `WEIGHT_PACKING=0.2`

Bu sayede scheduler pod'u cluster içinde çalışırken ağırlıklarını kod değişmeden dışarıdan alabilir hale geldi.

### 3.17. Güncellenmiş scheduler'ı cluster'a deploy edip smoke test yaptık

Refactor sonrası:

- Scheduler image'ı yeniden build edildi
- kind node'larına yüklendi
- Deployment yeniden apply edildi
- Scheduler rollout'u başarıyla tamamlandı

Loglarda scheduler'ın yeni weight'lerle başladığı doğrulandı:

- `weights={FreeCPU:1 FreeMem:1 PodPenalty:0.5 Packing:0.2}`

Ardından smoke workload tekrar çalıştırıldı.

Sonuç:

- Pod'lar başarıyla schedule edildi
- Scheduler log'larında artık bind kararlarıyla birlikte skor da yazıldı

Bu test, parametrik scoring refactor'unun canlı cluster'da regresyon yaratmadan çalıştığını doğruladı.

### 3.18. İlk evaluation harness iskeletini ekledik

Scoring refactor'undan sonra sıradaki ihtiyaç, farklı weight set'lerini aynı workload üzerinde tekrar tekrar koşturup ölçüm alabilecek bir evaluation katmanıydı.

Bu amaçla repo içine şu yapılar eklendi:

- [scheduler/cmd/evaluator/main.go](./scheduler/cmd/evaluator/main.go)
- [scheduler/pkg/evaluator/types.go](./scheduler/pkg/evaluator/types.go)
- [scheduler/pkg/evaluator/workload.go](./scheduler/pkg/evaluator/workload.go)
- [scheduler/pkg/evaluator/weights.go](./scheduler/pkg/evaluator/weights.go)
- [scheduler/pkg/evaluator/metrics.go](./scheduler/pkg/evaluator/metrics.go)
- [workloads/uniform-small.json](./workloads/uniform-small.json)

İlk evaluator sürümü şunları yapar:

1. JSON workload dosyasını okur
2. İstenirse scheduler deployment weight'lerini günceller
3. Workload pod'larını sırayla cluster'a oluşturur
4. Pod scheduling ve completion zamanlarını toplar
5. Özet metrikleri JSON olarak üretir
6. Test pod'larını sonunda temizler

Bu ilk sürümde üretilen temel metrikler:

- `averagePendingSeconds`
- `p95PendingSeconds`
- `unschedulableCount`
- `maxActiveNodes`

### 3.19. Workload formatını sabitledik

İlk harness için workload formatı sade bir JSON liste olarak seçildi.

Her workload item şu alanları içerir:

- `name`
- `arrivalSeconds`
- `cpuMilli`
- `memoryMiB`
- `durationSeconds`

Bu format evaluator tarafından okunur ve aynı senaryo farklı weight set'leriyle tekrar tekrar oynatılabilir.

### 3.20. Evaluator smoke run yaptık

İlk doğrulama için [workloads/uniform-small.json](./workloads/uniform-small.json) kullanıldı.

Evaluator şu komutla çalıştırıldı:

- `go run ./cmd/evaluator --workload ../workloads/uniform-small.json --timeout 90s --output /tmp/evo-sched-eval.json`

Sonuç:

- Workload başarıyla cluster'a basıldı
- Tüm pod'lar schedule edildi
- Tüm pod'lar tamamlandı
- JSON metrik çıktısı üretildi
- Evaluator namespace'i temiz bırakıldı

Örnek özet sonuç:

- `scheduledCount=4`
- `completedCount=4`
- `unschedulableCount=0`
- `averagePendingSeconds=1.5`
- `p95PendingSeconds=2`
- `maxActiveNodes=2`

Bu, scheduler'ın artık yalnızca çalışan bir sistem değil, aynı zamanda ölçülebilen ve karşılaştırılabilen bir deney nesnesine dönüştüğünü gösterir.

### 3.21. Evaluator'ı batch experiment runner'a çevirdik

Tek koşuluk evaluator, birden fazla weight set'i aynı workload üzerinde art arda koşturabilecek şekilde genişletildi.

Bu amaçla şu eklemeler yapıldı:

- `--weights-file` desteği
- tek JSON içinde çoklu run sonucu üretme
- CSV karşılaştırma çıktısı üretme
- her run öncesi evaluator namespace temizleme
- her run öncesi scheduler weight patch + rollout wait
- batch sonunda scheduler'ı başlangıç weight'lerine geri döndürme

Yeni giriş dosyası formatı için [experiments/weights-smoke.json](./experiments/weights-smoke.json) eklendi.

Bu dosya üç baseline policy içerir:

- `baseline-default`
- `spread-heavy`
- `packing-heavy`

### 3.22. Workload set'ini genişlettik

İlk `uniform-small` workload'una ek olarak iki yeni workload daha tanımlandı:

- [workloads/mixed-batchy.json](./workloads/mixed-batchy.json)
- [workloads/bursty.json](./workloads/bursty.json)

Bu sayede evaluator artık sadece tek tip küçük pod'larla değil, farklı boyutlar ve daha ani yük desenleriyle de çalıştırılabilir.

### 3.23. Batch smoke experiment yaptık

İlk batch doğrulama için şu komut kullanıldı:

- `go run ./cmd/evaluator --workload ../workloads/uniform-small.json --weights-file ../experiments/weights-smoke.json --timeout 90s --output /tmp/evo-batch.json`

Bu koşu sonunda:

- JSON sonuç dosyası üretildi
- CSV karşılaştırma dosyası üretildi
- Üç farklı weight set aynı workload üzerinde art arda koşturuldu
- Evaluator namespace temiz bırakıldı
- Scheduler deployment weight'leri tekrar default değerlerine döndürüldü

Örnek özet çıktı:

- `baseline-default`: `avgPending=1.0`, `p95Pending=1.85`, `maxActiveNodes=2`
- `spread-heavy`: `avgPending=1.5`, `p95Pending=2.0`, `maxActiveNodes=2`
- `packing-heavy`: `avgPending=1.5`, `p95Pending=2.0`, `maxActiveNodes=2`

Bu nokta çok önemli; çünkü artık "aynı workload, farklı weight set, sabit ölçüm çıktısı" döngüsü kurulmuş oldu. Yani evaluator artık gerçek bir experiment runner davranışı sergiliyor.

## 4. Şu Anki Durum

Şu anda proje durumu şöyledir:

- 1. katmanın çekirdek akışı çalışıyor
- Custom scheduler cluster içinde deploy edilmiş durumda
- `schedulerName: evok8s-scheduler` kullanan pod'lar custom scheduler tarafından schedule edilebiliyor
- Scheduler artık uygun olmayan node'ları filtreliyor
- Scheduler artık parametreli bir skor fonksiyonu ile node seçiyor
- Skor ağırlıkları env üzerinden değiştirilebiliyor
- Resource fit kontrolü scoring akışına dahil edilmiş durumda
- Birden fazla weight set'i art arda koşturabilen batch evaluator mevcut
- JSON ve CSV sonuç üretilebiliyor
- Çoklu workload set'i tanımlanmış durumda

Başka bir ifadeyle:

- Adım 1 tamam
- Adım 2 tamam
- Adım 3 tamam
- Adım 4 ilk başarılı smoke test seviyesiyle tamam
- Adım 4 için temel feasibility filtreleri de eklenmiş durumda
- Adım 5 için gereken parametrik policy zemini kurulmuş durumda
- Adım 6 ve 7 için gerekli evaluation altyapısının ilk kullanışlı sürümü kurulmuş durumda

## 5. Sıradaki Muhtemel Adımlar

Buradan sonra mantıklı ilerleme sırası şudur:

1. Batch evaluator sonuçlarını daha büyük workload set'lerinde toplamak
2. Fitness ölçümünü netleştirmek
3. Evolutionary optimization katmanına geçmek
4. Baseline scheduler policy'leri ile karşılaştırma yapmak
5. Sonuçları tablo/grafik üretimine bağlamak

İlk teknik iyileştirme adayları:

- Fitness için hangi metriklerin birleştirileceğini netleştirmek
- Batch sonuçları tablo/grafik üretimine bağlamak
- Evolutionary loop için population ve mutation mantığını eklemek

## 6. Bu Dosya Nasıl Kullanılmalı

Her anlamlı session sonunda bu dosya güncellenmelidir.

Önerilen güncelleme düzeni:

- Önce amaç ve büyük resim sabit tutulur
- Sonra "bu session öncesi durum" kısa şekilde revize edilir
- Ardından yeni session'da yapılan somut işler eklenir
- En altta güncel durum ve sıradaki adım net biçimde yazılır

Bu sayede dosya hem ilerleme günlüğü hem de projeye geri döndüğümüzde hızlı onboarding rehberi olarak kullanılabilir.
