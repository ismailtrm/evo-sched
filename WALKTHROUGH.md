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

### 3.24. Capacity-relative workload desteği ekledik

İlk 3x3 deney matrisinden sonra önemli bir sınıra ulaştık:

- weight'ler pending davranışını değiştiriyor
- fakat `maxActiveNodes` metriği mevcut cluster ve hafif workload'larda sabit kalıyor

Bu nedenle workload tanımını cluster kapasitesine daha taşınabilir hale getirdik.

[scheduler/pkg/evaluator/types.go](./scheduler/pkg/evaluator/types.go) ve [scheduler/pkg/evaluator/workload.go](./scheduler/pkg/evaluator/workload.go) içinde şu destek eklendi:

- workload item'larında `cpuFraction`
- workload item'larında `memoryFraction`
- evaluator tarafında worker allocatable kapasitesinden `WorkloadBase` çıkarma
- fractional istekleri gerçek `cpuMilli` ve `memoryMiB` değerlerine çözümleme

Bu tasarımın amacı şudur:

- workload dosyaları sabit makine boyutuna bağımlı kalmaz
- aynı senaryo farklı cluster'larda yeniden üretilebilir hale gelir
- `resource-pressure` workload'ları worker kapasitesinin yüzdesiyle ifade edilebilir

Evaluator batch çıktısına artık şu bilgi de eklenir:

- kullanılan `WorkloadBase`
- resolve edilmiş gerçek workload listesi

Bu sayede deney sonucu yalnızca "hangi dosya çalıştı" değil, "gerçekte hangi pod resource request'leri üretildi" bilgisini de taşır.

### 3.25. Resource-pressure workload ailesini ekledik

Policy farkının `active-node` ekseninde de görünmesi için yeni workload setleri eklendi:

- [workloads/cpu-tight.json](./workloads/cpu-tight.json)
- [workloads/mem-tight.json](./workloads/mem-tight.json)
- [workloads/fragmenting-mix.json](./workloads/fragmenting-mix.json)

Bu workload'lar absolute request yerine worker kapasitesinin yüzdesini kullanır.

Amaçları:

- `cpu-tight`: CPU baskısında packing ve spread davranışını ayırmak
- `mem-tight`: memory baskısında aynı ayrımı görmek
- `fragmenting-mix`: CPU-heavy ve memory-heavy pod'lar birlikteyken oluşan fragmentation riskini gözlemek

Bu workload ailesi, mevcut hafif workload set'lerini çöpe atmaz; aksine onları "light-load baseline" olarak bırakır ve üstüne daha baskılı bir deney katmanı ekler.

### 3.26. 4-worker kind cluster config ekledik

Mevcut cluster'ı bozmadan daha geniş deneyler için ayrı bir kind config dosyası eklendi:

- [kind-config-4workers.yaml](./kind-config-4workers.yaml)

Bu dosya:

- 1 control-plane
- 4 worker

içerir.

Bu tercih şu nedenle yapıldı:

- mevcut çalışan cluster'ı korumak
- ama `packing` ile `spread` arasındaki node-count farkını daha görünür hale getirecek daha büyük bir cluster seçeneğini repo içinde hazır tutmak

İstenirse yeni cluster şu mantıkla oluşturulabilir:

- `kind create cluster --name evo-sched-4w --config kind-config-4workers.yaml`

### 3.27. İlk fractional workload smoke run'ını yaptık

Yeni capacity-relative workload formatını doğrulamak için [workloads/cpu-tight.json](./workloads/cpu-tight.json) ile evaluator çalıştırıldı.

Kullanılan komut:

- `go run ./cmd/evaluator --workload ../workloads/cpu-tight.json --weights-file ../experiments/weights-smoke.json --timeout 150s --output /tmp/evo-cpu-tight.json`

Bu koşuda evaluator mevcut 2-worker cluster'dan şu `WorkloadBase` değerlerini çıkardı:

- `workerCount=2`
- `workerCPUMilli=16000`
- `workerMemoryMiB=31714`

Buna göre `cpu-tight` pod'ları şu gerçek request'lere çözüldü:

- `cpuMilli=7200`
- `memoryMiB=2538`

Sonuç:

- tüm pod'lar schedule edildi
- tüm pod'lar tamamlandı
- evaluator namespace temiz bırakıldı
- scheduler weight'leri tekrar default değerlere döndü

Bu smoke run'ın amacı metric farkı üretmek değil, yeni workload formatının cluster kapasitesine göre doğru çözümlendiğini doğrulamaktı.

### 3.28. 4-worker cluster üzerinde ilk resource-pressure matrix'i koşturduk

Bir sonraki kritik deney için cluster topolojisi 4 worker olacak şekilde yeniden kuruldu.

Uygulanan akış:

- mevcut `evo-sched` kind cluster silindi
- [kind-config-4workers.yaml](./kind-config-4workers.yaml) ile aynı isimde yeni cluster oluşturuldu
- scheduler image'ı yeniden build edildi
- image yeni cluster node'larına yüklendi
- [rbac.yaml](./rbac.yaml) ve [deploy.yaml](./deploy.yaml) yeniden apply edildi
- scheduler pod'unun rollout'u beklendi
- [test-workload.yaml](./test-workload.yaml) ile kısa smoke validation yapıldı

Smoke validation sonucu:

- scheduler pod'u `Running`
- smoke pod'ları başarıyla bind edildi
- bind kararları scheduler log'larında görüldü

Ardından ilk gerçek matrix şu kombinasyonla çalıştırıldı:

- workload'lar: `cpu-tight`, `mem-tight`, `fragmenting-mix`
- weight set'ler: `baseline-default`, `spread-heavy`, `packing-heavy`
- tekrar sayısı: her kombinasyon için `3`

Kontrol disiplini için her batch öncesi ve sonrası şu bilgiler loglandı:

- ready worker sayısı
- scheduler deployment env weight'leri
- evaluator namespace pod sayısı

Bu kontrol log'u şurada saklanır:

- [results/2026-03-15-4worker-resource-pressure/controls.log](./results/2026-03-15-4worker-resource-pressure/controls.log)

Özet sonuçlar şu dosyalara kaydedildi:

- [results/2026-03-15-4worker-resource-pressure/summary.csv](./results/2026-03-15-4worker-resource-pressure/summary.csv)
- [results/2026-03-15-4worker-resource-pressure/summary.json](./results/2026-03-15-4worker-resource-pressure/summary.json)
- [results/2026-03-15-4worker-resource-pressure/detail.csv](./results/2026-03-15-4worker-resource-pressure/detail.csv)

Bu deneyden çıkan ilk kritik bulgular:

- `active-node` tradeoff nihayet açıldı
- `packing-heavy`, `cpu-tight` ve `mem-tight` altında `maxActiveNodes=2` seviyesine inerken `spread-heavy` ve çoğu durumda `baseline-default` `maxActiveNodes=4` kullandı
- `fragmenting-mix` altında `packing-heavy` orta bir davranış göstererek `maxActiveNodes=3` kullandı

Pending tarafındaki okuma:

- `cpu-tight` altında `packing-heavy` ve `spread-heavy` baseline'dan daha düşük pending üretti
- `mem-tight` altında pending metrikleri neredeyse aynı kaldı; fakat `packing-heavy` daha az node ile aynı pending'i verdi
- `fragmenting-mix` altında en düşük pending `spread-heavy`, ikinci `packing-heavy`, en zayıf ise `baseline-default` oldu

Bu sonuç çok önemlidir; çünkü artık scheduler policy weight'leri hem pending hem de node kullanımında görünür tradeoff üretmektedir. Yani proje gerçekten çok hedefli scheduler optimization karakterine ulaşmıştır.

### 3.29. Optimizer iskeletini ekledik

Tradeoff yüzeyi deneysel olarak doğrulandıktan sonra bir sonraki doğal adım evolutionary search katmanını eklemekti.

Bu amaçla:

- evaluator runtime'ı [scheduler/pkg/evaluator/runner.go](./scheduler/pkg/evaluator/runner.go) içine çıkarıldı
- [scheduler/cmd/evaluator/main.go](./scheduler/cmd/evaluator/main.go) ince bir CLI wrapper haline getirildi
- yeni [scheduler/pkg/optimizer](./scheduler/pkg/optimizer) paketi eklendi
- yeni [scheduler/cmd/optimizer/main.go](./scheduler/cmd/optimizer/main.go) komutu eklendi

Optimizer tarafında ilk sürüm şu yapıyı kullanır:

- birey: `FreeCPU`, `FreeMem`, `PodPenalty`, `Packing`
- objective 1: workload'lar üzerindeki ortalama `p95PendingSeconds`
- objective 2: workload'lar üzerindeki ortalama `maxActiveNodes`
- selection: nondominated sort + crowding distance
- variation: uniform crossover + gaussian mutation
- elitism: NSGA-II benzeri population update

Bu tasarım sayesinde optimizer artık gerçek cluster evaluator'ını doğrudan çağırabilir.

Yani kavramsal olarak:

- `evaluate(weights) -> {avgP95Pending, avgMaxActiveNodes}`

akışı kod içinde kurulmuş oldu.

Optimizer komutu baseline policy'leri de aynı çıktı içine dahil eder:

- `baseline-default`
- `spread-heavy`
- `packing-heavy`

ve bunları optimizer adaylarıyla aynı Pareto analizine koyar.

### 3.30. İlk optimizer smoke run'ını yaptık

Kod entegrasyonunu doğrulamak için küçük bir smoke run çalıştırıldı.

Kullanılan komut:

- `go run ./cmd/optimizer --workloads ../workloads/cpu-tight.json --baseline-file ../experiments/weights-smoke.json --population 2 --generations 1 --seed 42 --timeout 120s --output /tmp/evo-optimizer-smoke.json`

Bu smoke run sonunda:

- JSON sonuç dosyası üretildi
- combined Pareto CSV üretildi
- all-candidates CSV üretildi
- evaluator namespace temiz kaldı
- scheduler weight'leri tekrar default değerlere döndü

Bu aşamanın amacı tam ölçekli optimization sonucu almak değil, evolutionary loop'un evaluator entegrasyonu ile cluster üstünde gerçekten çalıştığını doğrulamaktı.

### 3.31. Scheduler deployment weight update akışını conflict retry ile sağlamlaştırdık

İlk uzun optimizer koşusunda scheduler deployment env'leri güncellenirken ara sıra Kubernetes `resourceVersion` conflict hatası görüldü.

Hata biçimi kabaca şuydu:

- `Operation cannot be fulfilled on deployments.apps "evok8s-scheduler": the object has been modified`

Bu durum, evaluator'ın scheduler deployment'ını `Get` edip `Update` etmesi ile deployment controller'ın aynı nesneyi eşzamanlı güncellemesi arasında yarış oluşabildiğini gösterdi.

Bunu düzeltmek için [scheduler/pkg/evaluator/runner.go](./scheduler/pkg/evaluator/runner.go) içindeki `applyWeights(...)` akışı güçlendirildi:

- deployment her denemede yeniden `Get` edilir
- `Update` sırasında conflict alınırsa kısa backoff ile tekrar denenir
- en fazla 5 denemeden sonra hata döndürülür

Ardından optimizer smoke run'ı tekrar çalıştırıldı:

- `go run ./cmd/optimizer --workloads ../workloads/cpu-tight.json --baseline-file ../experiments/weights-smoke.json --population 2 --generations 1 --seed 42 --timeout 120s --output /tmp/evo-optimizer-smoke-retry.json`

Sonuç:

- smoke run başarıyla tamamlandı
- JSON ve CSV çıktıları üretildi
- evaluator namespace temiz kaldı
- scheduler weight'leri tekrar default değerlere restore edildi

Bu düzeltme, uzun optimizer deneylerinin deployment update çakışmaları yüzünden yarıda kalma riskini belirgin biçimde azalttı.

### 3.32. Danışman geri bildirimi ile araştırma yönünü netleştirdik

Bu aşamada danışmandan proje kapsamı, ana objective'ler ve dönem sonu için yeterli başarı ölçütü hakkında net geri bildirim alındı.

Sorulan ana sorular ve alınan cevaplar özetle şöyledir:

1. Ana araştırma çıktısı ne olmalı?

- Ana çıktı `Pareto front` olmalı.
- Tek bir birleşik skor ana çıktı olarak kullanılmamalı.
- İstenirse raporda yardımcı analiz olarak tek bir "uzlaşma skoru" verilebilir.

2. Hangi metrikler ana objective olarak tutulmalı?

- Ana objective'ler:
  - `p95 pending time`
  - `max active nodes`
- Yardımcı metrikler:
  - `unschedulable count/rate`
  - `load variance`

3. Baseline set'i yeterli mi?

- Mevcut üç baseline bu dönem için yeterli kabul edildi:
  - `baseline-default`
  - `spread-heavy`
  - `packing-heavy`
- `default kube-scheduler` ile karşılaştırma değerli bir güçlendirme olur, ama bu dönem için zorunlu değil.

4. Sentetik workload yeterli mi?

- Evet, bu dönem için yeterli.
- Mevcut sentetik workload'lar tekrar üretilebilir ve tradeoff'u görünür kılıyor.
- Gerçek trace tabanlı workload, gelecekteki genişletme olarak düşünülebilir.

5. Kaç tekrar yeterli?

- Matrix deneyleri için `3` tekrar yeterli kabul edildi.
- Optimizer için en az `3` seed önerildi:
  - `42`
  - `123`
  - `999`
- Zaman kalırsa `5` seed daha güçlü raporlama sağlar.

6. Optimizer tarafında hedef tek çözüm mü, çözüm kümesi mi?

- Hedef tek en iyi çözüm değil, `Pareto kümesi` olmalı.
- Rapor içinde bu kümeden üç temsilci çözüm seçilmeli:
  - pending-odaklı en iyi çözüm
  - node-odaklı en iyi çözüm
  - dengeli uzlaşma çözümü
- Dengeli uzlaşma çözümü için ana seçim yöntemi `knee point` olacak.
- Gerekirse normalize edilmiş birleşik skor yalnızca ikincil doğrulama için kullanılacak.

7. Bu dönem için makul bitiş noktası nedir?

- Çalışan gerçek custom scheduler
- Tradeoff'un deneysel olarak gösterilmesi
- Evolutionary search ile baseline'ların ötesinde yeni Pareto noktaları bulunması

8. kind üzerinde deney yapmak metodolojik olarak yeterli mi?

- Evet, kontrollü ve tekrar üretilebilir Kubernetes testbed olarak yeterli kabul edildi.
- Raporda açıkça şu sınır yazılmalı:
  - sonuçlar metodolojik karşılaştırma içindir
  - üretim ortamına doğrudan genelleme iddia edilmemektedir

9. Raporlama ağırlığı sistem mi, optimizasyon mu olmalı?

- Yaklaşık denge:
  - `%35` sistem altyapısı
  - `%65` optimizasyon ve deney metodolojisi

10. Projedeki ana akademik değer nedir?

- Ana değer: workload'a göre optimize edilen scheduling policy
- Çarpan etkisi: bu policy'nin gerçek Kubernetes scheduler olarak çalışıyor olması

Bu görüşmeden sonra netleşen kararlar şunlardır:

- Ana analiz ekseni `Pareto front` olacak.
- Ana objective set'i sabitlendi:
  - `p95 pending time`
  - `max active nodes`
- Yardımcı metrikler raporda tutulacak:
  - `unschedulable count/rate`
  - `load variance`
- Bu dönem için sentetik workload yeterli kabul edildi.
- Bu dönem için mevcut üç baseline yeterli kabul edildi.
- `default kube-scheduler` karşılaştırması opsiyonel güçlendirme olarak bırakıldı.
- Bu dönem için hedef, evolutionary optimizer'ın baseline'ların ötesinde yeni Pareto noktaları bulduğunu göstermek.
- Pareto içinden seçilecek dengeli temsilci çözüm `knee point` yaklaşımı ile belirlenecek.
- Tam optimizer değerlendirmesi en az üç seed ile koşturulacak:
  - `42`
  - `123`
  - `999`

### 3.33. İlk tam optimizer koşusunu (`seed=42`) tamamlayıp özetledik

Danışman geri bildirimi sonrasında ilk tam optimizer koşusu şu ayarlarla tamamlandı:

- population size: `12`
- generations: `5`
- workloads:
  - `cpu-tight`
  - `mem-tight`
  - `fragmenting-mix`
- seed: `42`

Bu koşunun çıktıları şu klasöre kaydedildi:

- [results/2026-03-15-optimizer-seed42/result.json](./results/2026-03-15-optimizer-seed42/result.json)
- [results/2026-03-15-optimizer-seed42/result-pareto.csv](./results/2026-03-15-optimizer-seed42/result-pareto.csv)
- [results/2026-03-15-optimizer-seed42/result-all.csv](./results/2026-03-15-optimizer-seed42/result-all.csv)

Toplam değerlendirme sayıları:

- baseline sayısı: `3`
- optimizer tarafından değerlendirilen aday sayısı: `72`
- baseline dahil toplam çözüm sayısı: `75`
- final population boyutu: `12`
- optimizer-only nondominated çözüm sayısı: `3`
- baseline dahil birleşik nondominated çözüm sayısı: `4`

Birleşik Pareto front'ta kalan çözümler:

- `gen-03-008`
- `gen-03-009`
- `gen-05-012`
- `packing-heavy`

Ancak raporlama açısından daha önemli olan şey, birey sayısı değil benzersiz objective noktalarıdır.

`seed=42` için birleşik Pareto front iki benzersiz tradeoff noktasına çöktü:

1. `avgP95Pending = 0.6667`, `avgMaxActiveNodes = 4`
2. `avgP95Pending = 1.0000`, `avgMaxActiveNodes = 2.3333`

Bu sonuç, Pareto yüzeyinin bu seed için iki baskın rejime ayrıldığını gösterdi:

- düşük pending / yüksek node kullanımı
- daha düşük node kullanımı / biraz daha yüksek pending

Baseline durumu:

- Pareto'da kalan baseline:
  - `packing-heavy`
- Pareto'dan düşen baseline'lar:
  - `baseline-default`
  - `spread-heavy`

Özellikle `spread-heavy` objective noktasının optimizer tarafından domine edildiği görüldü.

Yeni optimizer bulgusu:

- Optimizer, `packing-heavy` ile aynı düşük-node rejimini yeniden keşfetti:
  - `gen-05-012`
- Ayrıca baseline set'te olmayan yeni bir düşük-pending rejimi buldu:
  - `gen-03-008`
  - `gen-03-009`

Bu yeni rejim şu objective noktasını verdi:

- `avgP95Pending = 0.6667`
- `avgMaxActiveNodes = 4`

Temsilci çözümler:

- pending-odaklı en iyi çözüm:
  - `gen-03-008` veya `gen-03-009`
- node-odaklı en iyi çözüm:
  - `packing-heavy` veya aynı objective noktayı bulan `gen-05-012`

`seed=42` için strict anlamda tekil bir `knee point` çıkmadı.
Sebep, birleşik Pareto front'un yalnızca iki benzersiz objective noktasından oluşmasıdır.

Bu nedenle bu seed için çıkarılan ana araştırma cümlesi şudur:

- optimizer, baseline set'in ötesinde yeni bir baskın düşük-pending rejimi buldu
- düşük-node rejiminde ise `packing-heavy` davranışını yeniden keşfetti
- Pareto yapısı bu seed altında iki baskın tradeoff rejimine ayrıldı
  - düşük pending / yüksek node kullanımı
  - daha düşük node kullanımı / biraz daha yüksek pending

### 3.34. Optimizer hiperparametrelerini makul başlangıç varsayımları olarak tuttuk

Bu çalışmada evolutionary optimizer hiperparametreleri ayrı bir tuning problemi olarak ele alınmamıştır.

Başka bir ifadeyle, bu projede optimize edilen şey scheduler policy weight'leridir:

- `FreeCPU`
- `FreeMem`
- `PodPenalty`
- `Packing`

Buna karşılık optimizer tarafındaki bazı seçimler sabit başlangıç varsayımları olarak tutulmuştur:

- offspring üretiminde `uniform crossover`
- her offspring için crossover uygulanması
- her gen için mutasyon olasılığı `0.8`
- Gauss gürültüsü ölçeği `0.25`
- weight aralığı için clamp sınırları `0.0` ile `2.0`

Bu seçimler rastgele yapılmamıştır; problem yapısına göre makul ilk varsayımlar olarak seçilmiştir.

Seçim mantığı şöyledir:

- arama uzayı küçüktür; yalnızca `4` weight optimize edilmektedir
- buna karşılık her evaluation gerçek cluster üzerinde pahalıdır
- bu nedenle sınırlı sayıda aday ile yeterli keşif yapılması gerekir
- erken daralmayı önlemek için mutasyon görece yüksek tutulmuştur
- `sigma = 0.25`, `[0, 2]` aralığında ne çok küçük ne de aşırı yıkıcı bir adım büyüklüğü vermektedir

Bu nedenle mevcut optimizer ayarları şu şekilde yorumlanmalıdır:

- bunlar nihai olarak tune edilmiş en iyi hyperparameter set'i değildir
- ilk deneyler için kararlı ve savunulabilir başlangıç ayarlarıdır

Bu not rapor açısından önemlidir; çünkü burada ana araştırma değişkeninin optimizer hiperparametreleri değil, scheduling policy weight'leri olduğu açıkça ortaya konmuş olur.

Gelecekteki doğal genişletme:

- `mutation rate`
- `mutation sigma`
- farklı crossover / offspring üretim stratejileri

için ayrı bir sensitivity analysis yapılmasıdır.

### 3.35. Metodoloji denetimi sonrasında `v1` sonuçlarını pilot olarak yeniden sınıflandırdık

Paper ve olası KubeCon başvurusu için iddiaları güçlendirmeden önce mevcut deney protokolü yeniden denetlendi.

Bu denetim, `seed=42` sonucunu silmeyi veya değersiz saymayı değil, kanıt seviyesini doğru adlandırmayı amaçladı.

Tespit edilen ilk metodolojik risk policy uzayındaki bağımlılıktır.

Mevcut packing özelliği şöyledir:

```text
packing = 1 - average(remainingCPUAfterPlacement / allocCPU,
                      remainingMemAfterPlacement / allocMem)
```

Homojen worker node'larda ve aynı Pod request'i aday node'lar arasında karşılaştırılırken bu terim yaklaşık olarak şu biçime açılır:

```text
packing = candidate-independent constant
          - 0.5 * normalizedFreeCPU
          - 0.5 * normalizedFreeMem
```

Bu durumda aday sıralamasını belirleyen skor şu efektif biçime yaklaşır:

```text
score = constant
        + (wFreeCPU - 0.5 * wPacking) * normalizedFreeCPU
        + (wFreeMem - 0.5 * wPacking) * normalizedFreeMem
        - wPodPenalty * normalizedPodCount
```

Bunun iki sonucu vardır:

- `Packing`, mevcut homojen kind topolojisinde `FreeCPU` ve `FreeMem` sinyallerinden tamamen bağımsız davranmayabilir
- tüm weight'leri aynı pozitif katsayıyla çarpmak node sıralamasını değiştirmediği için farklı kromozomlar aynı policy'yi temsil edebilir

Dolayısıyla nominal olarak dört boyutlu olan arama uzayının efektif boyutu daha düşük olabilir.
Farklı bireylerin aynı objective noktasına çökmesi yalnızca bu bağımlılığı kanıtlamaz; ancak bu olasılığın test edilmesini zorunlu kılar.

İkinci risk ölçüm çözünürlüğü ve örnek büyüklüğüdür:

- scheduler sabit `2s` sleep kullanan polling loop ile çalışmaktadır
- evaluator Pod durumunu poll etse de pending değerini gözlem anından değil `PodScheduled` condition zamanından hesaplamaktadır
- buna rağmen mevcut sonuçlarda pending değerleri büyük ölçüde tam saniyelere çökmüştür
- workload'lar yalnızca `4`, `4` ve `6` Pod içermektedir
- optimizer içindeki her aday yalnızca bir kez değerlendirilmiştir

Bu nedenle `0.67s` ile `1.00s` gibi küçük farkların ne kadarının policy davranışı, ne kadarının scheduler cadence ve timestamp çözünürlüğü olduğunu ayırmadan güçlü latency iddiası kurulamaz.

Üçüncü risk workload baskısıdır:

- mevcut workload'lar node yerleşim karakterini görünür kılmıştır
- ancak Pod sayıları robust bir p95 dağılımı üretmek için küçüktür
- tüm ölçümlerde `unschedulable=0` kalmıştır
- optimizer'ın kapasite sınırı, kuyruk ve fragmentation davranışını yeterince zorlayıp zorlamadığı ayrıca doğrulanmalıdır

Dördüncü risk dış baseline eksikliğidir:

- mevcut üç preset custom policy uzayın anlamlı bölgelerini temsil etmektedir
- ancak default `kube-scheduler` henüz deneysel referans olarak ölçülmemiştir
- custom scheduler'ın polling modeli ile event-driven default scheduler arasında ham pending karşılaştırmasının adil olmayacağı ayrıca açıklanmalıdır

Bu denetim sonrasında şu karar alındı:

- `seed=42` ham sonuçları değiştirilmeyecek ve korunacak
- bu sonuçlar `v1 pilot` kanıtı olarak sınıflandırılacak
- daha önce planlanan `seed=123` ve `seed=999` koşuları iptal edilmedi, fakat `v2` policy ve ölçüm protokolü tamamlanana kadar ertelendi
- aynı metodolojik sorunları taşıyan pahalı `v1` tekrarları yerine önce arama uzayı ve ölçüm zemini düzeltilecek
- final paper ve CFP iddiaları yalnızca clean Git state üzerinde üretilen yeni official deneylerle güncellenecek

Bu karar önceki planla bilinçli bir yön değişikliğidir.
Önceki plan çoklu seed ile stokastik kararlılığı test etmeyi amaçlıyordu; yeni denetim ise seed kararlılığından önce ölçülen policy uzayının ve latency sinyalinin geçerli olması gerektiğini gösterdi.

### 3.36. Agent-driven araştırma çalışma usulünü sabitledik

Uzun süren bu çalışmada sohbet geçmişi tek doğruluk kaynağı olarak kabul edilmeyecektir.
Kalıcı kaynaklar şunlardır:

- Git commit geçmişi
- `WALKTHROUGH.md`
- commitlenmiş workload ve experiment config'leri
- değiştirilmeyen ham sonuç klasörleri
- her official deney için makine tarafından okunabilir manifest

Deneyler üç sınıfa ayrıldı:

- `official`: paper kanıtı olmaya aday; clean ve commitlenmiş tree gerektirir
- `pilot`: karar vermeyi sağlayan keşif sonucu; final doğrulama değildir
- `debug`: yalnızca sistem veya kod hatasını teşhis eder

Official deney hard gate'i:

1. working tree temiz olmalı
2. kod, workload, config ve analiz şeması commitlenmiş olmalı
3. Git SHA ve branch manifestte kaydedilmeli
4. exact command, seed, tekrar ve explicit flag'ler kaydedilmeli
5. input checksum'ları, cluster topolojisi, scheduler image/config ve sürümler kaydedilmeli
6. cluster ve scheduler preflight başarılı olmalı
7. output klasörü yeni olmalı veya resume kararı açıkça kayıtlı olmalı

Dirty tree üzerinde alınan bir sonuç sessizce official kabul edilmeyecek; açıkça `pilot` veya `debug` olarak işaretlenecek.

Git modeli hibrit olarak sabitlendi:

- küçük dokümantasyon ve bakım değişiklikleri `main` üzerinde atomik commit olabilir
- policy, measurement veya workload davranışını değiştiren işler kısa ömürlü `research/...` branch'lerinde yapılır
- official deney yalnızca testleri geçmiş ve stable branch'e birleşmiş commit üzerinden çalışır
- önemli deney protokolleri annotated Git tag ile dondurulur

Commit ve kanıt sırası:

1. test ve implementation commit'i
2. değiştirilmeyen raw result commit'i
3. türetilmiş analiz commit'i
4. `WALKTHROUGH.md` ve paper yorumu commit'i

Context kaybı veya reboot sonrasında recovery sırası:

1. `git status` ve current branch
2. son commitler
3. bu dosyadaki current state ve next gate
4. son experiment manifesti ve result klasörü
5. cluster preflight

Bu çalışma usulünü desteklemek için üç yerel Codex skill'i eklendi:

- `research-experiment-ledger`: provenance, hard gate, manifest ve interruption recovery
- `policy-space-audit`: weight invariance, feature bağımlılığı ve efektif arama uzayı denetimi
- `paper-evidence-audit`: paper claim, tablo ve grafiklerini commitlenmiş kanıtlarla karşılaştırma

Mevcut operasyon skill'lerinin sorumlulukları korunmuştur:

- `kubernetes`: genel Kubernetes tasarım, deploy ve troubleshooting
- `k8s-scheduler-experiment-ops`: scheduler deney preflight ve postflight
- `multi-seed-experiment-runner`: seed'leri sırayla ve manifestli çalıştırma
- `pareto-experiment-analysis`: unique Pareto noktaları ve baseline karşılaştırması

Paper kaynakları da Git'e alınmıştır:

- kaynak dosyalar, TikZ figürleri ve gerekli `IEEEtran.cls` izlenmektedir
- LaTeX build çıktıları ve indirilen template paketi `.gitignore` kapsamındadır
- sürekli değişen `main.pdf` repo yerine submission/release artifact olarak üretilecektir
- bu ayrım `3173ae9` commit'i ile kaydedilmiştir

### 3.37. Related-work araştırması için SciSpace plugin'ini ekledik

Paper'ın related-work ve kaynak doğrulama aşamasında akademik literatür keşfini kolaylaştırmak için SciSpace plugin'i kullanıcı onayıyla kuruldu.

Plugin'in görev sınırı şöyledir:

- Kubernetes scheduling, multi-objective optimization ve evolutionary scheduling çalışmalarını keşfetmek
- aday paper'ları konu, yöntem ve deney protokolü açısından ön elemek
- related-work için karşılaştırılabilecek katkı ve sınırlılıkları çıkarmaya yardımcı olmak

SciSpace deney kanıtı veya doğrulanmış kaynak yerine kullanılmayacaktır:

- Paper'daki teknik iddialar mümkün olduğunca özgün araştırma makalesi veya resmî Kubernetes dokümantasyonu ile desteklenecek
- Plugin özetleri doğrudan kaynak kabul edilmeyecek
- Kullanılacak her referansın başlığı, yazarları, yayın bilgisi ve ilgili iddiası birincil kaynaktan doğrulanacak
- Related-work notları paper'a girmeden önce `paper-evidence-audit` sürecinden geçirilecek

Bu aşamada ek task-management veya dosya-yedekleme plugin'i kurulmadı.
Roadmap için Git ve `WALKTHROUGH.md`, artifact saklama için repo ve release düzeni tek doğruluk kaynağı olarak korunmaktadır.

### 3.38. Gate 0 provenance ve paper evidence audit'ini tamamladık

`v1` pilot sonuçlarını değiştirmeden izlenebilir hale getirmek için iki geriye dönük manifest eklendi:

- [results/2026-03-15-4worker-resource-pressure/manifest.json](./results/2026-03-15-4worker-resource-pressure/manifest.json)
- [results/2026-03-15-optimizer-seed42/manifest.json](./results/2026-03-15-optimizer-seed42/manifest.json)

Manifestlerde şu bilgiler ayrıştırıldı:

- artifact'in üretildiği tarih ve kaydedildiği commit
- zaman çizelgesine göre yüksek güvenle eşleştirilen execution commit
- workload, baseline, optimizer ve source blob kimlikleri
- raw artifact SHA-256 değerleri
- cluster topolojisi ve doğrulanabilen toolchain bilgileri
- geriye dönük doğrulanamayan exact command, dirty state ve runtime sürümleri

Belirsiz alanlar tahmin edilmedi; açıkça `null` bırakıldı.
Bu nedenle manifestler provenance'ı iyileştirir, fakat historical run'ları yeni hard gate açısından `official` hale getirmez.

Ardından [paper/evidence-audit-v1.json](./paper/evidence-audit-v1.json) ile paper'daki ana sayısal ve metodolojik iddialar ham artifact'lere bağlandı.

Desteklenen `v1 pilot` bulguları:

- Packing, `cpu-tight` ve `mem-tight` altında mean occupied worker sayısını `4`ten `2`ye indirdi
- dokuz baseline kombinasyonunda bütün Pod'lar schedule edilip tamamlandı ve `unschedulable=0` kaldı
- `seed=42` artifact'i gerçekten `72` optimizer adayı, `75` toplam ölçüm, `4` combined nondominated üye ve `2` unique objective noktası içeriyor
- Packing aynı optimizer stream içinde Pareto'da kalırken Default ve Spread domine edildi
- paper tablo ve grafiklerindeki sayılar raw artifact'lerle rounding sonrasında eşleşiyor

Audit sırasında çıkan kritik düzeltmeler:

1. Repeated baseline matrix ana objective'lerde bir tradeoff göstermiyor.
   - Packing ve Spread üç workload'un tamamında aynı mean p95 değerine sahip
   - Packing her üçünde de daha az workload-occupied worker kullanıyor
   - bu nedenle Packing, repeated baseline matrix'te Spread'i zayıf biçimde domine ediyor

2. Dört weight nominal olarak dört bağımsız policy boyutu oluşturmuyor.
   - homojen worker'larda mevcut Packing sinyali FreeCPU ve FreeMem ile doğrusal bağımlı hale geliyor
   - tüm weight'lerin pozitif ortak ölçeklenmesi node sıralamasını koruyor

3. Optimizer-only düşük-pending noktası artifact içinde gerçektir, fakat robust final bulgu değildir.
   - her aday yalnızca bir kez değerlendirildi
   - workload'lar `4`, `4` ve `6` Pod içeriyor
   - ilgili workload-level p95 değerleri `0`, `1` ve `1` saniyeye çökmüş durumda
   - scheduler cadence ve timestamp çözünürlüğü ayrıştırılmadı

4. `maxActiveNodes` gerçek active/powered node sayısı değildir.
   - evaluator'ın ölçtüğü şey non-terminal evaluator Pod'u barındıran distinct worker sayısıdır
   - `v2` adı `max workload-occupied worker nodes` olarak netleştirilecektir

5. `v1` workload'lar aggregate cluster saturation üretmiyor.
   - CPU-tight toplamda yaklaşık `1.80` worker CPU'su
   - mem-tight toplamda yaklaşık `1.80` worker memory'si
   - fragmenting-mix her boyutta yaklaşık `1.76` worker kapasitesi istiyor
   - bunlar 4-worker cluster'da placement/consolidation pressure üretir, fakat whole-cluster capacity pressure değildir

6. Pending metriğinin tam timestamp kaynağı paper'da daha açık yazılmalıdır.
   - evaluator, Pod creation timestamp ile `PodScheduled=True` condition `LastTransitionTime` farkını kullanır
   - doğrudan ilk görülen `spec.nodeName` veya evaluator poll zamanı kullanılmaz

Gate 0 kararı:

- raw `v1` artifact'ler immutable pilot snapshot olarak korunacak
- mevcut paper sayısal olarak trace edilebilir, fakat final submission evidence olarak bloklu kabul edilecek
- `seed=123/999` mevcut `v1` üzerinde koşturulmayacak
- bir sonraki çalışma `research/policy-space-v2` branch'inde Gate 1 invariance testleri ile başlayacak
- final paper claim'leri ancak official `v2` kanıtı geldikten sonra güncellenecek

### 3.39. Gate 1 policy-space invariance testlerini ekledik

Gate 0 audit'inde cebirsel olarak görülen policy-space tekrarlarını çalıştırılabilir testlerle sabitlemek için
[scheduler/policy_space_test.go](./scheduler/policy_space_test.go) eklendi.
Bu adım üretim scoring davranışını değiştirmedi; önce mevcut `v1` modelinin efektif boyutunu kanıtlamayı amaçladı.

Eklenen tanı testleri:

- `TestV1PolicyPositiveScalingPreservesNodeRanking`
  - bütün weight'lerin aynı pozitif katsayıyla çarpılmasının node seçimini değiştirmediğini doğruluyor
  - pairwise score farklarının yalnızca aynı katsayıyla ölçeklendiğini kontrol ediyor
- `TestV1PackingWeightIsAbsorbedByFreeWeightsOnHomogeneousNodes`
  - aynı allocatable CPU ve memory kapasitesine sahip worker'larda `Packing` etkisinin `FreeCPU` ve `FreeMem` katsayılarına emilebildiğini doğruluyor
  - eşdeğer temsil `FreeCPU' = FreeCPU - Packing/2`, `FreeMem' = FreeMem - Packing/2`, `Packing' = 0` biçiminde kuruluyor
- `TestV1PackingDependencyIsConditionalOnHomogeneousAllocations`
  - aynı dönüşümün farklı allocatable kapasitelere sahip node'larda genel olarak geçerli olmadığını gösteriyor
  - böylece bulgunun tüm cluster topolojilerine taşınmadığı, mevcut homojen kind worker düzenine koşullu olduğu kaydediliyor

Homojen worker'lar ve aynı Pod request'i altında aday sıralamasını belirleyen skor şu efektif biçime indirgenebilir:

```text
score_i = C
        + (wFreeCPU - wPacking/2) * normFreeCPU_i
        + (wFreeMem - wPacking/2) * normFreeMem_i
        - wPodPenalty * normPodCount_i
```

Buradaki `C`, aday node'dan bağımsızdır ve node sıralamasını değiştirmez.
Ayrıca bütün efektif katsayıların aynı pozitif değerle çarpılması sıralamayı korur.
Dolayısıyla `v1` optimizer'ı nominal olarak dört weight arasa da homojen testbed'de dört bağımsız davranış boyutu aramıyordu.

Doğrulama:

```bash
cd scheduler
go test -short -cover ./...
```

Sonuç:

- bütün paketler geçti
- ana scheduler paketi statement coverage: `%48.0`
- optimizer paketi statement coverage: `%82.0`
- tanı testleri `04164c6` (`test: document v1 policy-space invariances`) commit'inde kaydedildi

Gate 1 ara kararı:

- scale invariance ve homojen-worker Packing bağımlılığı artık yalnızca yorum değil, regression test ile korunan bir bulgudur
- bir sonraki adım weight temsilinin ölçeğini sabitlemek ve operasyonel olarak bağımsız yeni sinyali literatür ile sistem semantiğine göre seçmektir
- yeni sinyal seçilmeden ve feature-independence testleri geçmeden resmi optimizer koşusu başlatılmayacaktır

### 3.40. Yerel `gopls` cache izin hatasını giderdik

Gate 1 testleri üzerinde çalışırken VS Code Go extension language server'ı başlatamadı.
Birincil hata şuydu:

```text
gopls cannot access its persistent index: mkdir /home/ismail/.cache/gopls/11d335bd: permission denied
```

Ardından görülen `write EPIPE`, `connection got disposed` ve `startFailed` mesajları birincil cache hatasının ikincil sonuçlarıydı.
Disk kullanımı normaldi; kök neden `/home/ismail/.cache/gopls` dizininin `nobody:nobody` sahipliğinde ve `0700` modunda olmasıydı.

Geri dönüşü kolay tutmak için hatalı cache silinmedi:

- eski dizin `/home/ismail/.cache/gopls.permission-backup-20260827-001433` adına taşındı
- yeni `/home/ismail/.cache/gopls` dizini `ismail:ismail` sahipliği ve `0700` modu ile oluşturuldu
- VS Code penceresi `Developer: Reload Window` ile yeniden yüklendi

Doğrulama:

- `gopls v0.23.0` gerçek kullanıcı ortamında başladı
- `gopls check main.go` sıfır hata ile tamamlandı
- `gopls check policy_space_test.go` sıfır hata ile tamamlandı
- reload sonrasında VS Code durum çubuğu ve Go language server normale döndü

Bu olay araştırma kodunu veya deney artifact'lerini değiştirmedi; yalnızca yerel geliştirme ortamı düzeltildi.

### 3.41. Policy space `v2` feature setini seçtik

Gate 1 invariance kanıtından sonra eski `Packing` terimini yalnızca başka bir serbest-kaynak toplamıyla değiştirmek yeterli görülmedi.
Çünkü `FreeCPU`, `FreeMem` ve `PodPenalty` mevcut işaretleriyle birlikte spread yönünde çalışıyor;
Packing kaldırılıp yalnızca balance eklenirse workload-occupied worker sayısını azaltan açık bir consolidation sinyali kalmıyor.

Karar öncesinde dört aday sinyal ailesi değerlendirildi:

1. `RequestedToCapacityRatio` veya başka nonlinear utilization curve
   - Kubernetes'in resmi `NodeResourcesFit` seçeneklerinden biridir
   - fakat yine aynı CPU ve memory utilization değişkenlerinin dönüşümüdür; tek başına yeni operasyonel boyut sağlamaz
2. dominant-resource residual veya best-fit
   - doğrusal toplamdan farklı node sıralamaları üretebilir
   - ancak CPU-memory fragmentation ile workload-occupied worker hedefini aynı terimde birleştirir ve yorumu belirsizleştirir
3. post-placement resource balance
   - Kubernetes `v1.35` `NodeResourcesBalancedAllocation` yaklaşımı CPU ve memory kullanım oranları arasındaki farkı kullanır
   - nonlinear olduğu için iki serbest-kaynak terimine sabit katsayılarla emilemez
   - complementary CPU-heavy ve memory-heavy Pod davranışını görünür kılar
4. workload-node consolidation
   - aday node zaten bu scheduler'ın aktif bir Pod'unu barındırıyorsa ayrı bir bonus üretir
   - resource fraction'lardan bağımsız, doğrudan `max workload-occupied worker nodes` objective'inin placement karşılığıdır
   - kind worker container'ının powered-on olduğunu iddia etmez; yalnızca workload occupancy ölçer

Kaynak kontrolü:

- Kubernetes, `MostAllocated` yaklaşımını yüksek allocation'a sahip node'ları tercih ederek daha boş node'ları scale-down için hazırlayan bin-packing stratejisi olarak tanımlar:
  <https://kubernetes.io/docs/concepts/scheduling-eviction/resource-bin-packing/>
- Kubernetes `release-1.35` `NodeResourcesBalancedAllocation`, CPU ve memory requested/capacity oranlarının standard deviation'ını kullanır:
  <https://github.com/kubernetes/kubernetes/blob/release-1.35/pkg/scheduler/framework/plugins/noderesources/balanced_allocation.go>
- Weng ve diğerleri, klasik packing ile fragmentation'ın aynı kavram olmadığını ve fragmentation sinyalinin workload talebine bağlı ele alınması gerektiğini gösterir:
  <https://www.usenix.org/system/files/atc23-weng.pdf>

Seçilen `v2` chromosome:

```text
[FreeCPU, FreeMem, Balance, Consolidation]
```

Feature tanımları:

```text
postFreeCPU = (FreeCPU - podCPURequest) / AllocCPU
postFreeMem = (FreeMem - podMemRequest) / AllocMem

balance = 1 - abs(postFreeCPU - postFreeMem)

consolidation = 1, node bu scheduler'ın en az bir non-terminal Pod'unu barındırıyorsa
                0, aksi halde
```

Yeni skor:

```text
score = wFreeCPU       * postFreeCPU
      + wFreeMem       * postFreeMem
      + wBalance       * balance
      + wConsolidation * consolidation
```

Bütün feature'lar `[0,1]` aralığında olacaktır.
Ağırlıklar negatif olmayacak ve scheduler/evaluator sınırında L1 normalize edilerek toplamları `1` yapılacaktır.
Bu canonical simplex temsili, bütün weight'leri aynı pozitif katsayıyla çarpan eşdeğer genotype'ları kaldıracaktır.
Toplamı sıfır olan weight vector geçersiz sayılacaktır.

Eski feature'ların durumu:

- `Packing`, cebirsel bağımlılık nedeniyle `v2`den çıkarılacaktır
- `PodPenalty`, küçük ve büyük Pod'ları yalnızca adet üzerinden eşit saydığı ve yeni consolidation terimiyle iki farklı Pod-count sinyali oluşturacağı için çıkarılacaktır
- `v1` testleri, sonuçları ve baseline dosyaları historical pilot evidence olarak korunacaktır
- `v2` baseline ve output contract'ı yeni alan adlarıyla versioned olarak oluşturulacaktır; eski JSON dosyaları sessizce `v2` olarak yorumlanmayacaktır

Uygulama hard gate'i:

- dört feature'ın her biri en az bir deterministic representative state'te node sıralamasını tek başına değiştirmeli
- normalization scale-equivalent vector'ları aynı canonical weight setine dönüştürmeli
- deterministic tie-break korunmalı
- eski `v1` artifact'ler ve deney tanımları değiştirilmemeli

### 3.42. Policy space `v2`yi scheduler, evaluator ve optimizer boyunca uyguladık

Seçilen `v2` tasarımı tek bir score fonksiyonu değişikliği olarak bırakılmadı.
Scheduler'ın uyguladığı policy ile evaluator'ın patch ettiği, optimizer'ın ürettiği ve artifact'lerin raporladığı policy'nin aynı contract olması için ortak bir tip oluşturuldu.

Uygulama zinciri:

- `6c569d4 test: preserve v1 policy audit fixture`: Eski `v1` formülü production tiplerinden ayrılmış, self-contained historical regression fixture olarak korunmuştur.
- `0ef5093 feat: add canonical v2 scheduler scoring`: Scheduler score'u yeni dört feature'a geçirilmiş; managed Pod sayımı resource-usage sayımından ayrılmış; feature independence, resource fit ve tie-break davranışları test edilmiştir.
- `2cf6178 refactor: centralize v2 policy weights`: Ortak `pkg/policy.WeightSet` ve `policy.Version = "v2"` tanımlanmış; geçerli vector'lar L1 normalize edilmiş; negatif, `NaN`, sonsuz ve all-zero vector'lar reddedilmiştir.
- `aadd682 feat: integrate canonical v2 policy evaluation`: Evaluator ve optimizer ortak contract'a geçirilmiş; deployment patch, versioned input/output, candidate normalization, offspring repair ve exact cache key uygulanmıştır.

`aadd682` içindeki güvenlik ayrıntıları:

- Scheduler deployment patch'i yeni `WEIGHT_BALANCE` ve `WEIGHT_CONSOLIDATION` env alanlarını kullanmaktadır.
- JSON experiment input'u `policyVersion: "v2"` istemekte ve bilinmeyen/legacy alanları sessizce kabul etmemektedir.
- JSON ve CSV output'larına `policyVersion`/`policy_version` provenance alanı eklenmiştir.
- Optimizer adayları evaluation öncesinde canonical normalize edilmektedir.
- Mutation sonrası oluşabilecek all-zero offspring, ebeveynlerin canonical ortalamasıyla deterministik olarak onarılmaktadır.
- Cache key artık altı ondalığa yuvarlanmış metin yerine dört `float64` değerinin exact bit pattern'ini kullanmaktadır; birbirinden farklı yakın adaylar aynı evaluation cache kaydına çökmez.

Yeni baseline contract'ı [experiments/weights-v2.json](./experiments/weights-v2.json) içinde sürümlü olarak tanımlandı:

- `evok8s-v2-balanced = (0.25, 0.25, 0.25, 0.25)`
- `evok8s-v2-spread-heavy = (0.35, 0.35, 0.25, 0.05)`
- `evok8s-v2-consolidation-heavy = (0.10, 0.10, 0.20, 0.60)`

Eski [experiments/weights-smoke.json](./experiments/weights-smoke.json) değiştirilmedi.
Bu dosya ve mevcut `results/` içeriği `v1 pilot` kanıtı olarak kalmaktadır; yeni evaluator bunları `v2` input'u gibi yorumlamak yerine açık hata vermektedir.

Kod-seviyesi doğrulama:

```text
go test -count=1 -short -race ./...
go vet ./...
git diff --check
jq -e '.policyVersion == "v2" and (.runs | length == 3) and all(.runs[]; ((.weights.freeCPU + .weights.freeMem + .weights.balance + .weights.consolidation) - 1 | fabs) < 1e-12)' experiments/weights-v2.json
```

Sonuç:

- bütün Go paketleri önbelleksiz race testinden geçti
- `go vet ./...` sıfır bulgu ile tamamlandı
- `weights-v2.json` için policy version, run count ve weight toplamı `jq` ile doğrulandı
- v1 raw result, paper ve historical experiment dosyaları bu implementasyon commit'ine dahil edilmedi

Bu adımda kind cluster yeniden başlatılmadı ve deney koşturulmadı.
Dolayısıyla bunlar official sonuç değil, Gate 1'in kod ve contract implementasyonudur.
Gate 1'in kalan operasyonel çıkış koşulu, temiz commit üzerinden yapılacak hafif bir `debug` sınıfı live smoke testidir.

### 3.43. Gate 1 live integration smoke testini tamamladık

Bu koşu araştırma sonucu üretmek için değil, `v2` contract'ının gerçek Kubernetes akışında çalıştığını doğrulamak için `debug` sınıfında yapıldı.
Koşu öncesinde working tree temizdi ve branch `research/policy-space-v2`, Git SHA `0bf6464` idi.

Ortamın başlangıç durumu:

- Önceki kind container'ları artık Docker'da mevcut değildi; cluster resume edilemedi.
- [kind-config-4workers.yaml](./kind-config-4workers.yaml) ile temiz `evo-sched` cluster oluşturuldu.
- `kind v0.31.0` ve `kindest/node:v1.35.0` kullanıldı.
- Control plane ile dört worker'ın tamamı `Ready` olarak doğrulandı.
- Host `PATH` içinde `kubectl` bulunmadığı için cluster image'ındaki `/usr/bin/kubectl` geçici olarak `/tmp/evo-kubectl` yoluna kopyalandı.
- Geçici client ve cluster sürümü `v1.35.0` idi; kullanıcı PATH'i veya repo kalıcı olarak değiştirilmedi.

Cluster oluşturma komutu:

```text
kind create cluster \
  --name evo-sched \
  --config kind-config-4workers.yaml \
  --image kindest/node:v1.35.0 \
  --wait 180s
```

Scheduler image'ı mevcut [scheduler/Dockerfile](./scheduler/Dockerfile) ile build edildi:

```text
docker build -t evok8s-scheduler:0.1.0 .
kind load docker-image --name evo-sched evok8s-scheduler:0.1.0
```

Yerel image kimliği:

```text
sha256:56624343ea3f959d06fc21391e1d64cc09034b233b13b37f205560bd51e5ec05
```

Namespace, RBAC ve deployment uygulandıktan sonra rollout başarıyla tamamlandı.
Scheduler başlangıç log'u default raw weight'lerin canonical sonucunu doğruladı:

```text
scheduler started name=evok8s-scheduler interval=2s weights={FreeCPU:0.25 FreeMem:0.25 Balance:0.25 Consolidation:0.25}
```

Smoke komutu:

```text
go run ./cmd/evaluator \
  --workload ../workloads/uniform-small.json \
  --weights-file ../experiments/weights-v2.json \
  --timeout 60s \
  --output /tmp/evo-v2-gate1-smoke.json
```

Geçici çıktılar:

- `/tmp/evo-v2-gate1-smoke.json`
- `/tmp/evo-v2-gate1-smoke.csv`

Üç `v2` baseline'ın her biri için:

- `scheduledCount = 4`
- `completedCount = 4`
- `unschedulableCount = 0`
- top-level ve run-level `policyVersion = "v2"`
- CSV `policy_version = v2`

`uniform-small` altında üç policy de `averagePendingSeconds = 0.5`, `p95PendingSeconds = 1.0` ve `maxActiveNodes = 1` üretti.
Bu workload yalnızca wiring smoke olduğu ve dört Pod içerdiği için bu eşitlik policy kalitesi hakkında kanıt veya araştırma bulgusu olarak yorumlanmayacaktır.

Koşu sonrası güvenlik kontrolleri:

- Scheduler deployment env değerleri `0.25/0.25/0.25/0.25` başlangıç setine geri döndü.
- Restoration rollout başarıyla tamamlandı.
- `evo-sched-eval` namespace içinde Pod kalmadı.
- Working tree temiz kaldı.
- Beş kind container batarya ve RAM tüketmemesi için silinmeden durduruldu.

Container'ları durdurma komutu:

```text
docker stop evo-sched-control-plane evo-sched-worker evo-sched-worker2 evo-sched-worker3 evo-sched-worker4
```

Operasyonel borç:

- Mevcut `k8s-scheduler-experiment-ops` preflight helper'ı `PodPenalty/Packing` bekleyen `v1` flag'lerine bağlıdır.
- Bu smoke'ta V2 env ve readiness kontrolleri manuel yapıldı.
- Gate 2 veya daha sonraki resmi koşulardan önce helper V2 contract ve policy version kontrolüyle güncellenmelidir.

Gate 1 sonucu:

- Unit-level feature independence ve canonicalization testleri geçmiştir.
- Gerçek cluster'da patch, rollout, workload replay, versioned output, cleanup ve weight restoration akışı geçmiştir.
- Gate 1 tamamlanmıştır; sıradaki araştırma gate'i Measurement `v2`dir.

### 3.44. Scheduler experiment preflight skill'ini V2 contract'a güncelledik

Gate 1 smoke sırasında `k8s-scheduler-experiment-ops` helper'ının yalnızca eski `PodPenalty/Packing` alanlarını bildiği ve `kubectl` host `PATH` içinde yoksa kullanılamadığı görüldü.
Ayrıca evaluator namespace henüz oluşmamışsa `kubectl` NotFound cevabını JSON olarak parse etmeye çalışarak düşebiliyordu.

Repo dışında, `/home/ismail/.codex/skills/k8s-scheduler-experiment-ops` altında tutulan skill şu yeteneklerle güncellendi:

- `--kubectl` ile explicit client binary seçimi
- `--expect-workers` ile exact worker-count gate'i
- deployment env alanlarından `v1`, `v2` veya mixed/unknown contract tespiti
- `FreeCPU`, `FreeMem`, `Balance`, `Consolidation` V2 weight desteği
- raw weight yerine canonical L1-normalized weight karşılaştırması
- scheduler name, rollout, weight validity ve policy version kontrolleri
- eksik evaluator namespace'i boş kabul eden kontrollü NotFound davranışı
- text ve JSON çıktıda raw/canonical weights, contract version ve ayrı readiness check'leri

Bu sayede deployment'taki raw `1/1/1/1` ile evaluator restoration sonrası `0.25/0.25/0.25/0.25` aynı canonical policy olarak değerlendirilir.
Eksik veya mixed policy alanları, invalid/all-zero weights, yanlış worker count ve rollout problemi preflight'ı başarısız yapar.

Doğrulama:

- helper `python3 -m py_compile` ile syntax check'ten geçti
- skill `quick_validate.py` ile geçerli bulundu
- fake `kubectl` forward testinde dört Ready worker, V2 raw weights ve eksik evaluator namespace için `ready=true` üretildi
- aynı fixture `expect-workers=3` ile çalıştırıldığında `workerCountMatches=false` ve `ready=false` üretti
- kurulu skill dosyaları doğrulanan geçici kopyayla byte-for-byte karşılaştırıldı

Skill repo dışında olduğu için bu değişiklik doğrudan proje Git geçmişine girmez; bu bölüm değişikliğin durable project kaydıdır.
Cluster smoke sonrasında durdurulduğu için güncellenmiş helper'ın gerçek-cluster acceptance testi Gate 2 başlangıç preflight'ında yapılacaktır.

### 3.45. Measurement `v2` timestamp semantiğini düzelttik

Önceki risk değerlendirmesinde evaluator'ın `1s` polling interval'inin doğrudan pending ölçümünü kuantize ettiği varsayılmıştı.
Kod ve Kubernetes `v1.35` source audit'i bu açıklamanın eksik olduğunu gösterdi:

- evaluator mevcut `ScheduledAt` değerini Pod'u gördüğü local polling anından değil, `PodScheduled=True` condition içindeki `LastTransitionTime` alanından alıyordu
- Kubernetes binding storage, başarılı binding sırasında `PodScheduled=True` condition'ını oluşturuyor
- aynı binding path'i Binding metadata annotation'larını Pod'a atomik olarak kopyalıyor
- `PodCondition.LastTransitionTime` tipi `metav1.Time`dır
- `metav1.Time` JSON ve unstructured gösterimde `RFC3339` kullanır ve second-level precision taşır
- Gate 1 smoke çıktısındaki `createdAt` ve `scheduledAt` değerlerinin tamamı da tam saniyeye çökmüştür

Kaynaklar:

- Kubernetes `v1.35` Binding storage ve annotation merge davranışı:
  <https://github.com/kubernetes/kubernetes/blob/release-1.35/pkg/registry/core/pod/storage/storage.go>
- Kubernetes API `PodCondition.LastTransitionTime` tipi:
  <https://github.com/kubernetes/api/blob/v0.35.0/core/v1/types.go>
- `metav1.Time` RFC3339 serialization davranışı:
  <https://github.com/kubernetes/apimachinery/blob/v0.35.0/pkg/apis/meta/v1/time.go>

Düzeltilmiş risk ayrımı:

- Scheduler'ın sabit `2s` loop cadence'i gerçek scheduling latency'ye doğrudan eklenebilir.
- Kubernetes `CreationTimestamp` ve `PodScheduled.LastTransitionTime` çiftinin second-level precision'ı kısa latency farklarını kuantize eder.
- Evaluator polling interval'i bu condition timestamp'ini üretmez; fakat scheduler kararının evaluator tarafından ne zaman görüldüğünü ve occupancy sample sıklığını etkiler.
- Dolayısıyla cadence, Kubernetes timestamp precision ve evaluator observation lag ayrı ölçülmelidir.

Measurement `v2` kararı:

- Evaluator, API create isteğinden hemen önce high-resolution `submitted-at` annotation'ı ekleyecek.
- Scheduler, node seçimi tamamlanıp bind isteği başlamadan hemen önce high-resolution `decision-at` timestamp'i üretecek.
- `decision-at`, Binding metadata üzerinden aynı bind işlemi içinde Pod annotation'ına kopyalanacak; ek Pod patch yetkisi gerekmeyecek.
- Primary custom-policy latency `decision-at - submitted-at` olarak hesaplanacak.
- Kubernetes-native coarse pending metriği `PodScheduled.LastTransitionTime - CreationTimestamp` olarak ayrı tutulacak.
- Evaluator observation lag, Pod'un bound halinin ilk görüldüğü local zaman ile scheduler decision timestamp'i arasından ayrı hesaplanacak.
- Her output measurement protocol version ve timestamp source semantiğini taşıyacak.
- Scheduler loop interval `SCHEDULER_INTERVAL` ile configurable olacak; default davranış geriye uyumlu olarak `2s` kalacak.

Resource objective adı da gerçek semantiğe çekilecek:

- `maxActiveNodes` ifadesi node power state veya cluster autoscaler activity iddiası taşımaktadır
- mevcut ölçüm yalnızca evaluator workload'una ait non-terminal Pod'ları barındıran benzersiz worker node sayısıdır
- Measurement `v2` output ve optimizer objective adı `maxWorkloadOccupiedWorkerNodes` olacaktır

Default kube-scheduler karşılaştırma sınırı:

- Custom scheduler `decision-at` annotation'ını üretebilir; default kube-scheduler üretmez.
- Bu nedenle high-resolution decision latency doğrudan iki scheduler arasında adil ortak metrik olarak sunulmayacaktır.
- Default kube-scheduler karşılaştırmasında ortak Kubernetes-native coarse pending ve workload-occupied worker metriği raporlanacak.
- Custom policy search içinde primary objective high-resolution decision latency olabilir; paper iki protokolü aynı sayıymış gibi birleştirmeyecektir.

Test-first çıkış koşulları:

- timestamp format/parse round-trip nanosaniye bilgisini korumalı
- Binding action doğru decision ve policy annotation'larını taşımalı
- evaluator submit annotation'ı API creation timestamp'inden bağımsız high-resolution kaynak olmalı
- timing extraction üç timestamp kaynağını birbirine karıştırmamalı
- summary, decision annotation eksikliğini saymalı ve optimizer sessiz fallback yapmamalı
- scheduler interval invalid, zero veya negative value'ları reddetmeli
- JSON ve CSV measurement version ile açık metric adları taşımalı
- historical `v1` result artifact'leri değiştirilmemeli

## 4. Şu Anki Durum

Şu anda proje durumu şöyledir:

- 1. katmanın çekirdek akışı çalışıyor
- Custom scheduler cluster içine deploy edilip doğrulandı; kind container'ları şu anda durdurulmuş durumda
- `schedulerName: evok8s-scheduler` kullanan pod'lar custom scheduler tarafından schedule edilebiliyor
- Scheduler artık uygun olmayan node'ları filtreliyor
- Scheduler artık parametreli bir skor fonksiyonu ile node seçiyor
- Skor ağırlıkları env üzerinden değiştirilebiliyor
- Resource fit kontrolü scoring akışına dahil edilmiş durumda
- Birden fazla weight set'i art arda koşturabilen batch evaluator mevcut
- JSON ve CSV sonuç üretilebiliyor
- Çoklu workload set'i tanımlanmış durumda
- Workload'lar artık worker kapasitesine göre yüzdesel olarak tanımlanabiliyor
- `resource-pressure` workload ailesi repo'ya eklenmiş durumda
- Daha büyük deneyler için 4-worker cluster config hazır durumda
- 4-worker cluster üzerinde ilk resource-pressure matrix'i başarıyla koşturulmuş durumda
- Sonuçlar repo altında saklanmış durumda
- Optimizer iskeleti eklenmiş durumda
- Optimizer, gerçek evaluator'ı kullanarak cluster üstünde aday değerlendirebiliyor
- Danışman geri bildirimi ile ana objective ve raporlama yönü netleşmiş durumda
- Yardımcı metrik olarak `load variance` seçilmiş durumda
- Pareto içinden dengeli temsilci çözüm için `knee point` yaklaşımı seçilmiş durumda
- Tam ölçekli ilk optimizer koşusu (`seed=42`) tamamlanmış ve özetlenmiş durumda
- `seed=42` altında iki baskın Pareto rejimi gözlenmiş durumda
- `seed=42` ve mevcut resource-pressure matrix'i artık `v1 pilot` kanıtı olarak sınıflandırılmış durumda
- Policy-space redundancy ve measurement-resolution riskleri kayıt altına alınmış durumda
- `seed=123` ve `seed=999`, düzeltilmiş `v2` protokolüne kadar ertelenmiş durumda
- Official deneyler için clean Git SHA, manifest ve preflight hard gate'i kabul edilmiş durumda
- Hibrit branch ve atomik kanıt commit düzeni kabul edilmiş durumda
- Paper kaynakları Git ile izleniyor; build ürünleri ignore ediliyor
- Araştırma ledger, policy audit ve paper evidence audit skill'leri yerel Codex ortamına kurulmuş durumda
- SciSpace plugin'i related-work keşfi ve paper inceleme desteği için kurulmuş durumda
- Gate 0 için iki retrospective pilot manifesti eklenmiş durumda
- `v1` paper evidence audit'i tamamlanmış ve blocking bulgular makine-okunabilir biçimde kaydedilmiş durumda
- Gate 0 tamam
- Gate 1'in scale-invariance ve Packing-dependency tanı testleri tamamlanmış durumda
- Gate 1 için canonical weight normalization kararı verilmiş durumda
- Gate 1 `v2` feature seti `FreeCPU`, `FreeMem`, `Balance`, `Consolidation` olarak seçilmiş durumda
- Gate 1 test-first `v2` scheduler scoring implementasyonu tamamlanmış durumda
- Scheduler, evaluator ve optimizer ortak, sürümlü ve canonical `v2` weight contract'ını kullanıyor
- `v2` experiment input'u legacy alanları reddediyor; JSON ve CSV output'ları policy version taşıyor
- `v2` için üç yeni baseline [experiments/weights-v2.json](./experiments/weights-v2.json) içinde tanımlanmış durumda
- Gate 1 kod doğrulamaları race detector ve `go vet` ile geçmiştir
- Gate 1 live integration smoke testi temiz commit üzerinden geçmiştir
- Gate 1 tamamlanmıştır
- Sıradaki araştırma işi Gate 2 Measurement `v2`dir
- Official optimizer koşuları Gate 2-5 protokol çıkış koşullarına kadar blokludur
- `evo-sched` kind container'ları smoke sonrasında silinmeden durdurulmuştur
- Scheduler experiment preflight skill'i V2 contract, canonical weights ve exact worker-count kontrolüyle güncellenmiştir
- Güncellenen preflight helper'ın gerçek-cluster acceptance testi Gate 2 açılışında yapılacaktır
- Measurement `v2` timestamp semantiği source audit ile tanımlanmıştır
- Evaluator polling'in pending timestamp kaynağı olmadığı; cadence, second-level Kubernetes time ve observation lag'in ayrı riskler olduğu netleştirilmiştir
- Gate 2'nin sıradaki işi measurement contract testleri ve high-resolution scheduler decision instrumentation implementasyonudur

Başka bir ifadeyle:

- Adım 1 tamam
- Adım 2 tamam
- Adım 3 tamam
- Adım 4 ilk başarılı smoke test seviyesiyle tamam
- Adım 4 için temel feasibility filtreleri de eklenmiş durumda
- Adım 5 için gereken parametrik policy zemini kurulmuş durumda
- Adım 6 ve 7 için gerekli evaluation altyapısının ilk kullanışlı sürümü kurulmuş durumda
- Adım 6 için hafif workload'lara ek olarak baskılı workload ailesi de hazırlanmış durumda
- Adım 6 ve 7 için node-count tradeoff'u görünür hale gelmiş durumda
- Adım 8 için gerekli evolutionary loop iskeleti kurulmuş durumda
- Adım 8 için ilk tam seed sonucu alınmış ve Pareto özeti çıkarılmış durumda
- Adım 8'in final doğrulaması, `v2` policy ve measurement protokolü tamamlanana kadar beklemede

## 5. Sıradaki Muhtemel Adımlar

Yol haritası bağımlılık sırasına göre gate'lere ayrılmıştır.

### Gate 0 - `v1` pilotu dondur ve provenance'ı tamamla

- Mevcut `seed=42` ve matrix sonuçlarını değiştirmeden koru
- Sonuçları final validation yerine pilot olarak etiketle
- Eksik Git SHA, sürüm, cluster topolojisi ve exact command bilgisini bulunabildiği ölçüde geriye dönük kaydet
- Raw result ile paper claim arasındaki mevcut bağlantıları audit et

Çıkış koşulu:

- `v1` sonuçlarının neyi desteklediği ve neyi desteklemediği açık olmalı

### Gate 1 - Policy space `v2`

Durum:

- scale invariance testi tamamlandı
- homojen worker'larda Packing ranking-equivalence testi tamamlandı
- bağımlılığın heterojen kapasitelere genellenmediğini gösteren sınır testi tamamlandı
- weight normalization tasarımı nonnegative L1 simplex olarak seçildi
- bağımsız operasyonel feature'lar `Balance` ve `Consolidation` olarak seçildi
- test-first scheduler, evaluator ve optimizer implementasyonu tamamlandı
- policy input/output contract'ı `policyVersion: "v2"` ile sürümlendi
- scale-equivalent vector'lar evaluation öncesinde canonical normalize ediliyor
- dört feature'ın representative node kararlarını bağımsız değiştirdiği test edildi
- legacy `v1` input'ların sessiz migration'ı engellendi
- debug sınıfı live integration smoke testi tamamlandı

- [x] Mevcut score için scale invariance ve ranking-equivalence testleri ekle
- [x] `Packing` bağımlılığını cebirsel ve ampirik olarak doğrula
- [x] Weight normalization ile temsil ölçeğini sabitle
- [x] `Packing` yerine bağımsız operasyonel sinyaller seç
- [x] Her feature'ın en az bir temsilci durumda node sıralamasını bağımsız değiştirdiğini test et
- [x] Tie-breaking ve weight sınırlarını test et
- [x] Temiz commit üzerinden debug sınıfı live integration smoke testi yap

Karar:

- post-placement CPU-memory balance ile scheduler-managed workload consolidation iki ayrı feature olarak kullanılacak
- dominant-resource residual ve daha gelişmiş fragmentation sinyalleri bu sürümün dışında tutulacak

Çıkış koşulu:

- optimizer'ın aradığı parametrelerin efektif policy farklılığı ürettiği testlerle gösterilmeli
- temiz commit üzerinden scheduler patch, rollout, workload replay ve default weight restoration akışı smoke testte geçmeli

Sonuç:

- Gate 1 çıkış koşulları tamamlandı
- official deney başlatılmadı
- V2 preflight helper güncellendi; gerçek-cluster acceptance testi Gate 2 açılışında yapılacak

### Gate 2 - Measurement `v2`

- [ ] Scheduler loop interval'ini `SCHEDULER_INTERVAL` ile configurable yap
- [ ] Evaluator submission ve scheduler decision anlarını high-resolution annotation olarak kaydet
- [ ] Kubernetes condition zamanı, scheduler decision zamanı ve evaluator observation zamanını ayır
- [ ] Measurement protocol version ve timestamp source metadata'sını output'a ekle
- [ ] `maxActiveNodes` adını `maxWorkloadOccupiedWorkerNodes` olarak düzelt
- [ ] Scheduler'ın `2s` cadence etkisini tiny-control deneyle ölç
- [ ] Gerekirse event/watch tabanlı monitoring'i polling'e karşı değerlendir

Çıkış koşulu:

- Aynı policy tekrarlarında ölçüm tabanı, jitter ve timestamp çözünürlüğü raporlanabilir olmalı

### Gate 3 - Workload ve metrik seti `v2`

- Workload başına Pod sayısını p95 ve dağılım analizi için anlamlı seviyeye çıkar
- CPU-tight, mem-tight ve fragmentation rejimlerini koru fakat gerçek kapasite baskısı oluştur
- Optimizer'ın görmediği en az bir holdout workload tanımla
- `unschedulable` değerini güvenlik/feasibility kısıtı olarak tut
- Yardımcı metrik olarak load variance, utilization ve fragmentation ölçülerini değerlendir
- Workload checksum ve instantiate edilen mutlak resource request'lerini manifestte sakla

Çıkış koşulu:

- Policy farkları yalnızca birkaç Pod veya tek cadence tick'ine bağlı olmadan ölçülebilmeli

### Gate 4 - Baseline protokolü

- `evok8s-default`, `evok8s-spread-heavy` ve `evok8s-packing-heavy` presetlerini `v2` policy'ye göre yeniden tanımla
- Default `kube-scheduler`ı dış referans baseline olarak ekle
- Uygunsa kube-scheduler `NodeResourcesFit` / `MostAllocated` benzeri packing referansını ayrıca ölç
- Event-driven kube-scheduler ile polling custom scheduler arasındaki ham latency karşılaştırmasının sınırını açıkla
- Placement ve consolidation metriklerini ortak karşılaştırma zemini olarak önceliklendir

Çıkış koşulu:

- Her baseline aynı workload, cluster ve raporlama protokolü altında izlenebilir olmalı

### Gate 5 - Official optimization ve doğrulama

- Clean stable commit ve annotated protocol tag oluştur
- Preflight sonrasında `42`, `123`, `999` seed'lerini sequential çalıştır
- Her seed için unique nondominated objective noktalarını çıkar
- Finalist Pareto çözümlerini ve baseline'ları bağımsız tekrarlarla yeniden ölç
- Merkezi eğilim yanında dağılım, worst-case ve güven aralığı raporla
- Pareto yapısını all-measured solutions üzerinden yeniden kur
- Pending-best, node-best ve yeterli nokta varsa knee-point temsilcisini seç
- Holdout workload üzerinde genelleme kontrolü yap

Çıkış koşulu:

- Yeni Pareto rejimlerinin baseline ve seed gürültüsünün ötesinde tekrarlandığı gösterilmeli

### Gate 6 - Paper, repo ve CFP hazırlığı

- `paper-evidence-audit` ile her sayısal claim'i official artifact'e bağla
- Abstract, results ve conclusion'ı yalnızca final protokol sonuçlarıyla güncelle
- README, lisans, pinned sürümler ve tek komutlu reproduction akışı ekle
- Grafiklerde uncertainty, baseline ve seed bilgisini görünür yap
- Paper ve CFP anlatısını "evrensel en iyi scheduler" yerine canlı cluster üzerinde policy-space search sırasında öğrenilen tradeoff ve metodoloji dersleri üzerine kur

Çıkış koşulu:

- Repo, paper ve CFP metni aynı commitlenmiş kanıt setini referans göstermeli

## 6. Bu Dosya Nasıl Kullanılmalı

Her anlamlı session sonunda bu dosya güncellenmelidir.

Önerilen güncelleme düzeni:

- Önce amaç ve büyük resim sabit tutulur
- Sonra "bu session öncesi durum" kısa şekilde revize edilir
- Ardından yeni session'da yapılan somut işler eklenir
- En altta güncel durum ve sıradaki adım net biçimde yazılır

Yeni bir agent veya context recovery sırasında önce `## 4. Şu Anki Durum`, sonra `## 5. Sıradaki Muhtemel Adımlar` okunmalıdır.
Sonraki gate seçilmeden önce Git durumu ve son manifest doğrulanmalıdır.

Bu sayede dosya hem ilerleme günlüğü hem de projeye geri döndüğümüzde hızlı onboarding rehberi olarak kullanılabilir.
