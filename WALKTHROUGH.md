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

### 3.46. Measurement `v2` kod sözleşmesini ve optimizer guard'larını tamamladık

Amaç:

- Gate 2'de tanımlanan timestamp kaynaklarını çalışan scheduler/evaluator akışına taşımak
- kısa latency farklarını coarse Kubernetes timestamp'lerinden ayırmak
- optimizer'ın eksik ölçümü `0` gibi yorumlayarak sahte Pareto avantajı üretmesini engellemek
- resource objective'ini gerçek ölçüm semantiğiyle adlandırmak

Uygulama iki atomik kod commit'iyle yapıldı:

1. `1b3bf70 feat: instrument scheduler decision timing`
2. `2fdda38 feat: enforce v2 evaluation semantics`

İlk dilimde:

- ortak `measurement v2` annotation sözleşmesi eklendi
- evaluator API create isteğinden hemen önce RFC3339Nano `submitted-at` yazmaya başladı
- scheduler bind isteğinden hemen önce RFC3339Nano `decision-at` üretiyor
- Binding metadata aynı işlem içinde decision, policy version ve measurement version annotation'larını Pod'a taşıyor
- scheduler loop cadence'i `SCHEDULER_INTERVAL` üzerinden configurable hale geldi
- default cadence geriye uyumlu olarak `2s` kaldı; invalid, zero ve negative değerler reddediliyor

İkinci dilimde output semantiği ayrıştırıldı:

- primary custom-policy metriği `schedulingDecisionLatencySeconds = decision-at - submitted-at`
- Kubernetes-native coarse destek metriği `kubernetesPendingSeconds`
- evaluator polling etkisi `evaluatorObservationLagSeconds`
- resource metriği `maxWorkloadOccupiedWorkerNodes`
- occupancy sample'ları index map yerine gözlem timestamp'i taşıyan kayıtlar haline geldi
- occupied worker sayısı, run başında alınan Ready ve schedulable worker snapshot'ı üzerinde hesaplanıyor
- scheduler cadence, evaluator poll interval'i ve tüm timestamp source tanımları JSON `measurementProtocol` alanında kaydediliyor
- evaluator ve optimizer CSV'leri de policy/measurement version ile iki interval'i taşıyor

Optimizer objective sözleşmesi:

- objective 1: workload seti üzerindeki ortalama `p95SchedulingDecisionLatencySeconds`
- objective 2: workload seti üzerindeki ortalama `maxWorkloadOccupiedWorkerNodes`
- coarse Kubernetes pending ve evaluator observation lag objective değildir; destek ve teşhis metriğidir

Geçersiz evaluation guard'ları:

- boş workload sonucu reddedilir
- scheduled, unschedulable ve toplam Pod sayaçları tutarsızsa reddedilir
- tek bir unschedulable Pod varsa aday objective üretmez
- tüm Pod'lar tamamlanmamışsa aday objective üretmez
- her scheduled Pod için decision, Kubernetes scheduled condition ve evaluator observation timestamp'i yoksa reddedilir
- worker occupancy hiç gözlenmediyse veya snapshot worker sayısını aşıyorsa reddedilir
- batch'ler farklı measurement version, cadence veya source sözleşmesi taşıyorsa aggregate edilmez

Bu guard'ların nedeni:

- eksik decision timestamp'lerinde coarse pending'e sessiz fallback yapmak iki farklı ölçüm protokolünü tek objective gibi gösterecekti
- hiç Pod schedule etmeyen veya occupancy sample'ı kaçırılan bir aday `0` latency / `0` node ile yapay biçimde iyi görünebilirdi
- protocol drift'i kabul etmek farklı cadence ve polling çözünürlüklerini aynı Pareto uzayında karşılaştıracaktı

Doğrulama:

- `go test -count=1 -short -race ./...` geçti
- `go vet ./...` geçti
- `git diff --check` geçti
- timestamp source separation, measurement version mismatch, cadence parsing, CSV provenance, partial timestamp, unschedulable, incomplete run ve occupancy sınırları unit testlerle kapsandı

Kanıt durumu:

- bu commit'ler ölçüm kodu ve sözleşmesi kanıtıdır; henüz official performance sonucu değildir
- historical `v1` JSON/CSV artefact'leri değiştirilmedi
- durmuş kind cluster henüz yeni image ile live acceptance testinden geçirilmedi
- Gate 2'nin sıradaki işi temiz commit üzerinden live measurement smoke ve tiny-control cadence deneyidir

### 3.47. Gate 2 live acceptance ve cadence pilotunu tamamladık

Live acceptance sınıfı:

- run class: `debug`
- scheduler/evaluator kaynak SHA: `56b2ba2`
- scheduler image: `evok8s-scheduler:0.1.0`
- image ID: `sha256:e502784e7071c53e7ffc586a41ed68d24825836b25d318b4a662553e37ac4375`
- cluster: `kind-evo-sched`, Kubernetes `v1.35.0`, 4 Ready worker

Operasyon akışı:

1. Durdurulmuş beş kind container yeniden başlatıldı.
2. V2 preflight gerçek cluster üzerinde çalıştırıldı.
3. Yeni scheduler image temiz commit'ten build edilip tüm kind node'larına yüklendi.
4. Deployment rollout restart ve rollout status ile bloklayıcı doğrulandı.
5. Scheduler log'u `interval=2s policyVersion=v2 measurementVersion=v2` ve canonical weights gösterdi.
6. `uniform-small` workload'u üç V2 baseline ile debug smoke olarak çalıştırıldı.
7. Run sonunda canonical weights restoration ve boş evaluator namespace postflight ile doğrulandı.

Debug smoke komutu:

```bash
go run ./cmd/evaluator \
  --workload ../workloads/uniform-small.json \
  --weights-file ../experiments/weights-v2.json \
  --timeout 60s \
  --output /tmp/evo-v2-gate2-measurement-smoke.json
```

Smoke sonucu:

- üç baseline'ın her birinde `4/4` Pod scheduled ve completed
- toplam `12/12` decision, Kubernetes scheduled condition ve evaluator observation timestamp'i mevcut
- unschedulable count `0`
- JSON `measurementProtocol`, scheduler interval `2s` ve evaluator poll interval `1s` taşıdı
- CSV açık V2 metric adlarını ve iki interval'i taşıdı
- occupancy sample'ları gözlem timestamp'iyle üretildi

Önemli timestamp gözlemi:

- high-resolution decision latency aynı run içinde yaklaşık `0.50s` ve `1.50s` arasında alternasyon gösterdi
- Kubernetes condition timestamp'i tam saniyeye çöktüğü için örneğin `decision-at=...52.957Z` iken `PodScheduled.LastTransitionTime=...52.000Z` görülebildi
- bu fiziksel sıra tersliği scheduler kararıyla ilgili değildir; second-level timestamp temsilinin kuantizasyon etkisidir
- dolayısıyla coarse Kubernetes pending kısa custom-policy latency objective'i olarak kullanılamaz

Cadence pilot tasarımı:

- workload commit'i: `b89c671 test: add cadence control workload`
- raw/summary result commit'i: `34adcd3 results: record v2 cadence pilot`
- artefact dizini: [results/2026-08-27-measurement-v2-cadence-pilot](./results/2026-08-27-measurement-v2-cadence-pilot)
- run class: `pilot`; paper evidence eligible değildir
- iki scheduler interval: `2s` ve `250ms`
- evaluator poll interval: iki grupta da `250ms`
- her interval için `3` tekrar
- her tekrarda `12` tiny Pod; grup başına `36` decision latency gözlemi
- weights iki grupta da canonical balanced `0.25,0.25,0.25,0.25`
- tüm Pod'lar scheduled/completed; unschedulable count `0`

Pooled Pod-level sonuç:

| Scheduler interval | Mean decision latency | Median | P95 | Std. dev. |
|---:|---:|---:|---:|---:|
| `250ms` | `0.3112s` | `0.3258s` | `0.4663s` | `0.1384s` |
| `2s` | `1.2790s` | `1.2256s` | `1.9623s` | `0.5243s` |

Cadence etkisi:

- `2s / 250ms` mean decision latency oranı `4.11x`
- `2s / 250ms` p95 decision latency oranı `4.21x`
- interval'i `2s`den `250ms`ye indirmek mean'i `%75.67`, p95'i `%76.24` düşürdü
- her iki grupta max workload-occupied worker count `1`; control workload kaynak tradeoff'u ölçmek için tasarlanmadı

Yorum:

- önceki `0-1-2s` pending bandının önemli bölümü scoring policy'den değil scheduler loop cadence'inden gelmektedir
- `250ms` grubunun mean latency'si interval'den biraz yüksek kaldığı için kalan gecikmede scheduler işi, API çağrıları ve loop fazı da rol oynar
- bu pilot policy üstünlüğü göstermez; measurement baseline ve implementation-latency etkisini kalibre eder
- official optimization'da scheduler interval tüm aday ve baseline'larda sabit tutulmalı ve manifestte kaydedilmelidir

Polling ile watch/event alternatifi kararı:

- primary decision latency evaluator'ın bound Pod'u ne zaman gördüğünden bağımsızdır
- evaluator polling etkisi ayrı `evaluatorObservationLagSeconds` metriğinde görünürdür
- occupancy metriği polling'e bağlı kalır ve çok kısa occupancy geçişlerini kaçırabilir
- mevcut pressure workload süreleri poll interval'den belirgin uzun olduğu için polling ilk official protokol için yeterli kabul edildi
- Gate 3'te minimum Pod duration / poll interval ilişkisi explicit protocol koşulu olarak kaydedilecek
- kısa ömürlü workload veya daha hassas occupancy ölçümü gerekirse watch/event monitoring yeniden değerlendirilecek
- bu nedenle Gate 2'de watch implementasyonu yapılmadı; primary objective'i iyileştirmeden reconnect ve event ordering karmaşıklığı ekleyecekti

Pilot sonrası güvenlik düzeltmesi:

- `9280c51 fix: reject invalid evaluator polling intervals`
- zero veya negative poll interval artık ticker panic'i yerine run başlamadan validation error üretir
- race testleri ve `go vet` yeniden geçti

Postflight:

- scheduler interval canonical `2s` değerine restore edildi
- scheduler weights canonical `0.25,0.25,0.25,0.25` değerine restore edildi
- 4 worker Ready ve scheduler rollout healthy
- evaluator namespace boş
- kind container'ları postflight doğrulamasından sonra silinmeden durduruldu
- Gate 2 çıkış koşulları tamamlandı

### 3.48. Gate 3 V2 workload ve destek metrik sözleşmesini hazırladık

Gate 3'ün amacı, V1 pilotundaki 4/4/6-Pod workload'ların ve yalnızca iki objective'in
oluşturduğu dar deney zeminini genişletmektir. Bu aşamada henüz official veya pilot
sonuç üretilmedi; önce ölçüm sözleşmesi ve input suite'i Git'te ayrı commit'lerle
sabitlendi:

1. `46da163 feat: record requested resource support metrics`
2. `3babc6f feat: add v2 pressure workload suite`

#### Requested-resource metric kararı

Kubernetes scheduler kararları Pod request ve Node allocatable kaynakları üzerinde
çalıştığı için evaluator'ın yeni metrikleri `actual utilization` olarak
adlandırılmayacaktır. Ölçülen değerler, yalnızca evaluator tarafından yaratılmış,
node'a bind edilmiş ve terminal olmayan Pod'ların request toplamlarıdır. Payda,
run başında alınan Ready/schedulable worker allocatable snapshot'ıdır.

JSON `measurementProtocol.workloadResourceMetricSource` alanı bu semantiği açıkça
taşır:

```text
non-terminal-bound-evaluator-pod-requests-over-run-start-worker-allocatable
```

Her evaluator poll sample'ında aşağıdaki değerler üretilir; run summary bunların
zaman içindeki aritmetik ortalamasını saklar:

- `meanWorkloadCPURequestedUtilization`
- `meanWorkloadMemoryRequestedUtilization`
- `meanWorkloadDominantRequestedLoadVariance`
- `meanWorkloadCPUToMemoryImbalance`

`dominant requested load`, worker başına `max(cpuRequestedRatio,
memoryRequestedRatio)` olarak hesaplanır. `load variance`, tüm worker'lar
üzerindeki population variance'tır; `CPU-to-memory imbalance` ise worker başına
CPU ve memory requested ratio farkının mutlak değerinin ortalamasıdır. Bu iki
metrik load distribution ve kaynak dengesizliğini gözlemek içindir. Pareto
objective'leri değişmedi:

- minimize `averageP95SchedulingDecisionLatency`
- minimize `averageMaxWorkloadOccupiedWorkerNodes`

Bu ayrım, destek metriğinin optimizer fitness'ini sessizce değiştirmesini engeller.
Workload-demand-conditioned bir fragmentation formülü bu gate'te eklenmedi;
`CPU-to-memory imbalance` açıkça sınırlı bir dengesizlik proxy'sidir, doğrudan
fragmentation ölçümü değildir.

#### Polling adequacy guard

Occupancy ve requested-resource sample'ları evaluator polling'e bağlı olduğundan,
minimum Pod duration / poll interval oranı output'a `workloadSamplingProfile`
olarak yazılır. Runner ve optimizer bu oran `2.0` altındaysa workload'u objective
üretmeden reddeder. Batch ve result row aynı sampling profile'ı taşımadığında da
aggregation reddedilir.

Bu, primary decision latency'nin polling ile ölçüldüğü anlamına gelmez; primary
latency hâlâ scheduler'ın `decision-at` annotation'ından gelir. Guard yalnızca
occupancy ve requested-resource destek metriklerinin yetersiz örneklenmesini
engeller.

Measurement version `v2` olarak korundu. Yeni alanlar primary timestamp veya
objective semantiğini değiştirmiyor; fakat eski V2 cadence pilotu yeni resource
source alanını taşımadığı için yeni optimizer aggregation'ına karışamaz. Böylece
historical pilot korunurken yeni Gate 3 artifact'leri farklı metadata sözleşmesiyle
ayrışır.

#### V2 pressure workload suite

Historical root-level workload dosyaları değiştirilmedi. Yeni suite
[`workloads/v2`](./workloads/v2) altında tanımlandı ve her dosya portable fraction
request'ler kullanır. Evaluator fraction'ları run başlangıcındaki worker snapshot'ına
göre mutlak milliCPU/MiB request'lere çözer.

| Workload | Rol | Pod ve burst düzeni | Her burst toplam request | Hedeflenen mekanizma |
| --- | --- | --- | --- | --- |
| `cpu-tight-train` | optimizer training | 2 x 12 Pod, t=0/16 s, 10 s duration | 2.64 CPU, 0.72 memory worker fraction | 4 Pod/node packing ile 3 worker; free-CPU spread ile 4 worker ayrımı |
| `mem-tight-train` | optimizer training | 2 x 12 Pod, t=0/16 s, 10 s duration | 0.72 CPU, 2.64 memory worker fraction | CPU-tight'in memory-eksenli karşılığı |
| `fragmenting-mix-train` | optimizer training | 2 x 6 CPU-heavy/memory-heavy çift, t=0/16 s, 10 s duration | 2.40 CPU, 2.40 memory worker fraction | tamamlayıcı CPU-memory istekleri altında balance/consolidation davranışı |
| `mixed-burst-holdout` | optimizer dışı holdout | 2 x 4 CPU/memory/balanced triplet, t=0/16 s, 10 s duration | 1.80 CPU, 1.80 memory worker fraction | train suite'inden farklı üçlü mix altında genelleme kontrolü |

Her dosya 24 Pod taşır. `250ms` evaluator poll interval'inde minimum duration/poll
oranı `40x`tır; iki burst arasındaki 16 saniye, ilk 10 saniyelik burstün terminal
olması için bilerek bırakılmıştır. Tablo yalnızca tasarım hedefini açıklar; 3 veya
4 worker occupancy ayrımı henüz canlı deney sonucu olarak iddia edilmemektedir.

`scheduler/pkg/evaluator/workload_v2_test.go` aşağıdaki contract'ı korur:

- dört V2 workload dosyası evaluator tarafından portable çözülmelidir
- her biri tam 24 Pod içermelidir
- minimum duration 10 saniye olmalıdır
- `250ms` poll interval için ratio en az `40x` olmalıdır

Kod doğrulaması:

```bash
cd scheduler
go test -count=1 -short -race ./...
go vet ./...
```

Bu kontroller Gate 3 code commit'i öncesi ve V2 workload testinden sonra geçti.

### 3.49. Gate 3 V2 debug live smoke'unu tamamladık

V2 suite ve support metric akışını gerçek cluster üzerinde ilk kez doğrulamak için
`debug` sınıfında tek bir acceptance run yapıldı. Ham output ve manifest önce,
ledger kaydı sonra commitlendi:

- artifact commit: `6dfb835 results: record v2 gate3 debug smoke`
- artifact directory:
  [`results/2026-08-27-v2-gate3-debug-smoke`](./results/2026-08-27-v2-gate3-debug-smoke)
- execution Git SHA: `cd3816c5ed3eced646d8221f9116ad18f4d5bef2`
- execution tree: clean
- class: `debug`
- `paperEvidenceEligible: false`

Koşu öncesi ve sonrası V2 preflight geçti: dört worker Ready, deployment rollout
healthy, canonical weights `0.25/0.25/0.25/0.25` ve evaluator namespace boştu.
Scheduler interval debug koşusu için `2s`den `250ms`ye alındı; evaluator poll
interval de `250ms`di. Run sonunda weights korunmuş, scheduler interval `2s`ye
restore edilmiş ve namespace tekrar boş doğrulanmıştır. Ardından beş kind container
resource tasarrufu için tekrar durduruldu.

Koşu komutu:

```bash
KUBECONFIG=/home/ismail/.kube/config go run ./cmd/evaluator \
  --workload ../workloads/v2/cpu-tight-train.json \
  --weights 1,1,1,1 \
  --poll-interval 250ms \
  --timeout 90s \
  --output /tmp/evo-v2-gate3-debug-cpu-tight.json
```

Canlı acceptance sonucu:

- resolved worker base: `16000m` CPU ve `31712MiB` memory
- her evaluator Pod request'i: `3520m` CPU ve `1903MiB` memory
- `24/24` Pod schedule edildi ve tamamlandı
- `unschedulableCount = 0`
- decision, Kubernetes scheduled ve evaluator observation timestamp count'ları
  ayrı ayrı `24`
- `123` occupancy sample ve `123` requested-resource sample yazıldı
- resource source, sampling profile ve yeni CSV kolonları JSON/CSV output'ta mevcut
- minimum duration/poll ratio `40x`

Bu artifact'te balanced policy için `maxWorkloadOccupiedWorkerNodes = 4` görüldü.
Bu tek policy, tek run ve debug sınıfı koşudan policy karşılaştırması veya baseline
üstünlüğü çıkarılmayacaktır.

Önemli debug gözlemi:

- scheduler interval `250ms` olmasına rağmen ikinci 12-Pod burstte decision latency
  tail'i yaklaşık `2.21s`e, summary p95'i `2.10s`e ulaştı
- bu gözlem burst altında scheduler loop/API interaction'ının tail latency ürettiğini
  düşündürüyor; scoring policy etkisi olarak yorumlanmıyor
- Gate 3 acceptance matrix'te her baseline için workload-level timing dağılımı,
  `maxWorkloadOccupiedWorkerNodes` ve destek metrikleri birlikte incelenecek

Gate 3 debug acceptance tamamlandı. Sıradaki araştırma koşusu clean commit üzerinden
üç V2 baseline x üç train workload acceptance matrix'i olacak; bu koşu `pilot`
olarak sınıflanacak ve official optimizer/seed deneyinden önce workload davranışını
kontrol etmek için kullanılacak.

### 3.50. İlk Gate 3 baseline pilotunu rollout yarışı nedeniyle geçersiz saydık

Üç baseline x üç train workload acceptance matrix'ine geçmeden önce
`cpu-tight-train` workload'u üzerinde üç V2 baseline'ı batch olarak çalıştırma
girişimi yapıldı. Koşu clean `b67344b0df413d6ea1531d2b30dc1aaedb6f1dee`
SHA'sından başlatıldı; preflight dört Ready worker, V2 policy, canonical weights ve
boş evaluator namespace doğrulamalarını geçti. Scheduler ve evaluator interval'i
`250ms` olarak ayarlanmıştı.

Bu girişim geçerli bir pilot sonucu üretmedi:

- evaluator süreci output JSON/CSV oluşturmadan kapandı
- evaluator namespace run sonrasında boştu
- scheduler loglarında aynı Pod için hem başarılı bind hem de
  `pod ... is already assigned to node ...` bind hataları görüldü
- bu nedenle kısmi gözlemler metrik veya policy kanıtı olarak korunmadı
- `results/` altında artifact üretilmedi ve run `paperEvidenceEligible` değildir

Cluster event ve deployment incelemesi kök nedeni rollout semantiğine bağladı.
Deployment tek replica olsa da varsayılan `RollingUpdate` stratejisi yeni scheduler
Pod'unu eski Pod tamamen kapanmadan başlatıyordu. Scheduler'da leader election
bulunmadığı için iki proses kısa süre aynı unscheduled Pod listesini işleyip bind
yarışına girebiliyordu. Evaluator'ın `deploymentReady` kontrolü de updated, ready ve
available replica sayılarını doğruluyor; fakat toplam replica sayısının desired
değere indiğini ve terminating replica kalmadığını kontrol etmiyordu.

Düzeltme test-first uygulandı:

1. desired replica `1` iken toplam replica `2` olan rollout'un hazır sayılmaması
   için kırmızı test eklendi.
2. `terminatingReplicas > 0` olan rollout'un hazır sayılmaması için kırmızı test
   eklendi.
3. `deploymentReady`, exact total/updated/ready/available replica convergence,
   sıfır unavailable replica ve sıfır terminating replica gerektirecek şekilde
   sıkılaştırıldı.
4. Deneysel tek-replica scheduler deployment stratejisi `Recreate` yapıldı; weight
   rollout'larında eski ve yeni scheduler proseslerinin örtüşmesi yapısal olarak
   engellendi.

Kod commit'i:

- `512dff8 fix: prevent overlapping scheduler rollouts`

Doğrulamalar:

```bash
cd scheduler
go test -count=1 -short -race ./...
go vet ./...
```

Her iki doğrulama ve `git diff --check` geçti. Kesilen girişimden sonra scheduler
canonical `0.25/0.25/0.25/0.25` weight'lerine ve `2s` interval'e döndürüldü, tek
scheduler Pod kaldığı doğrulandı ve beş kind container tekrar durduruldu. Gate 3
acceptance matrix maddesi bu nedenle hâlâ tamamlanmış sayılmamaktadır; pilot yeni
fix commit'i üzerinden baştan çalıştırılacaktır.

### 3.51. Gate 3 V2 baseline acceptance pilotunu tamamladık

Rollout overlap düzeltmesinden sonra üç V2 baseline x üç train workload matrisi
clean `aa5e3d9f984bee6eff537b3b57329321544c4d4c` SHA'sından yeniden çalıştırıldı.
Bu koşu workload feasibility, measurement completeness, support metric provenance
ve policy separation kabulü içindir; tek tekrarlı olduğu için final baseline veya
paper sonucu değildir.

Artifact sırası korunmuştur:

- results commit: `3ba119a results: record v2 gate3 baseline pilot`
- artifact directory:
  [`results/2026-08-27-v2-gate3-baseline-pilot`](./results/2026-08-27-v2-gate3-baseline-pilot)
- execution Git SHA: `aa5e3d9f984bee6eff537b3b57329321544c4d4c`
- execution tree: clean
- class: `pilot`
- `paperEvidenceEligible: false`

Her workload ayrı batch olarak şu ortak protokolle çalıştırıldı:

```bash
KUBECONFIG=/home/ismail/.kube/config go run ./cmd/evaluator \
  --workload ../workloads/v2/<workload>.json \
  --weights-file ../experiments/weights-v2.json \
  --poll-interval 250ms \
  --timeout 90s \
  --output /tmp/<output>.json
```

Canlı deployment'ın `Recreate` kullandığı ve exact-one-replica convergence sağladığı
koşu öncesinde doğrulandı. Her batch arasında evaluator'ın scheduler weight'lerini
canonical `0.25/0.25/0.25/0.25` değerlerine restore ettiği ve namespace'in boş
kaldığı preflight ile kontrol edildi. Son batch sonrasında scheduler interval
`2s`ye restore edildi, final postflight geçti ve beş kind container durduruldu.

Dokuz kombinasyonun tamamında:

- `24/24` Pod schedule edildi ve tamamlandı
- `unschedulableCount = 0`
- decision, Kubernetes scheduled ve evaluator observation timestamp count'ları
  `24/24` oldu
- minimum duration/poll ratio `40x` olarak korundu
- requested-resource source ve raw per-worker sample'lar output'ta yer aldı

Tek-run pilot özeti:

| Workload | Policy | p95 decision (s) | Max occupied workers | Mean dominant-load variance |
| --- | --- | ---: | ---: | ---: |
| `cpu-tight-train` | balanced | 1.220 | 4 | 0.0116 |
| `cpu-tight-train` | spread-heavy | 1.144 | 4 | 0.0035 |
| `cpu-tight-train` | consolidation-heavy | 1.225 | 3 | 0.1263 |
| `mem-tight-train` | balanced | 1.175 | 4 | 0.0111 |
| `mem-tight-train` | spread-heavy | 1.348 | 4 | 0.0043 |
| `mem-tight-train` | consolidation-heavy | 1.159 | 3 | 0.1267 |
| `fragmenting-mix-train` | balanced | 1.645 | 3 | 0.1066 |
| `fragmenting-mix-train` | spread-heavy | 1.216 | 4 | 0.0076 |
| `fragmenting-mix-train` | consolidation-heavy | 1.221 | 3 | 0.1053 |

Bu pilotun kabul açısından gösterdiği davranış:

- CPU ve memory tight workload'larda consolidation-heavy 3 worker, balanced ve
  spread-heavy 4 worker kullandı
- fragmenting-mix'te spread-heavy 4 worker ve düşük load variance üretirken
  balanced ile consolidation-heavy 3 worker kullandı
- spread-heavy'nin düşük variance, consolidation-heavy'nin yüksek variance
  üretmesi policy feature'larının placement karakterini değiştirdiğini gösterdi
- fragmenting-mix'te spread-heavy ile consolidation-heavy p95 değerleri yakınken
  occupied-worker sayısı ayrıştı

Bu maddeler yalnızca acceptance sinyalidir. Tek tekrar içindeki p95 sıralaması
stokastik jitter'dan ayrıştırılamaz; örneğin mem-tight altında consolidation-heavy
hem daha az worker hem daha düşük p95 üretmiş olsa da bu sonuç dominance claim'i
olarak kullanılmayacaktır. Aynı şekilde requested utilization ve dominant-load
variance gerçek donanım utilization'ı değil, Pod request'lerinden türetilen destek
metrikleridir. CPU-memory imbalance da doğrudan fragmentation ölçümü değildir.

Gate 3 kabul koşulu tamamlanmıştır: 24-Pod workload'lar feasibility kaybı olmadan
ölçülebilmiş, objective ve destek metrikleri eksiksiz üretilmiş ve farklı policy
karakterleri occupied-worker/variance eksenlerinde görünür olmuştur. Sıradaki iş
Gate 4 baseline protokolünü tamamlamak, özellikle default kube-scheduler dış
referansını aynı workload ve artifact sözleşmesine bağlamaktır.

### 3.52. Default kube-scheduler dış referans yolunu ekledik

Gate 4'te default kube-scheduler'ı custom policy'lerle aynı scheduler gibi
göstermemek için evaluator'a iki açık execution mode eklendi:

- `custom-policy`: deployment weight patch, custom high-resolution decision
  timestamp ve scheduler interval metadata'sı kullanılabilir
- `external-reference`: scheduler deployment'ına dokunulmaz; Pod'lar verilen
  `schedulerName` profiline gönderilir ve yalnızca ortak Kubernetes/placement
  gözlemleri toplanır

Kubernetes'in resmi scheduler configuration dokümantasyonu, varsayılan profil
adının `default-scheduler` olduğunu ve Pod `.spec.schedulerName` alanının profili
seçtiğini doğrular. Scheduling Framework dokümantasyonu da default scheduler'ın
birden çok filtering/scoring plugin'ini birlikte kullandığını açıklar. Bu nedenle
referans, tek bir `FreeCPU` veya packing heuristiği olarak değil, sürüme bağlı
default plugin bileşimi olarak sınıflandırılmıştır:

- [Scheduler Configuration](https://kubernetes.io/docs/reference/scheduling/config/)
- [Scheduling Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)

Implementasyon commit'i:

- `0e5d6b5 feat: add external scheduler reference evaluation`

Yeni evaluator command contract'ı:

```bash
go run ./cmd/evaluator \
  --workload ../workloads/v2/cpu-tight-train.json \
  --scheduler-mode external-reference \
  --scheduler-name default-scheduler \
  --run-name kube-scheduler-default \
  --poll-interval 250ms \
  --timeout 90s \
  --output /tmp/evo-v2-kube-scheduler-debug.json
```

External mode için output semantiği özellikle ayrıştırıldı:

- `policyVersion = not-applicable`
- `schedulerMode = external-reference`
- `weightsApplicable = false`; JSON'da weight nesnesi yazılmaz
- `schedulerDecisionTimestampAvailable = false`
- `schedulerIntervalApplicable = false`
- CSV'de weight, custom decision latency ve decision-to-observation lag alanları
  sıfır yerine boş bırakılır
- Kubernetes `PodScheduled` condition zamanı coarse secondary reference olarak
  korunur
- occupied-worker ve requested-resource metrikleri custom policy ile aynı source
  contract'ını kullanır

Bu çalışma sırasında full race testi daha önce coverage'a girmeyen bir evaluator
yarışını da yakaladı: creator goroutine `SubmittedAt/CreatedAt` yazarken monitor
aynı `PodTiming` kaydını okuyabiliyordu. Create-time yazımları mevcut result mutex'i
ile korundu; full `go test -count=1 -short -race ./...` ve `go vet ./...` temiz
geçti.

#### Live external-reference debug smoke

Yeni yol clean `0e5d6b55fa6dc380392675902db9f1dcff50af0e` SHA'sından 4-worker
kind cluster üzerinde `debug` sınıfında doğrulandı:

- results commit: `bca6508 results: record kube-scheduler reference smoke`
- artifact directory:
  [`results/2026-08-27-v2-kube-scheduler-debug-smoke`](./results/2026-08-27-v2-kube-scheduler-debug-smoke)
- `paperEvidenceEligible: false`

Cluster'daki `kube-scheduler` Pod'u Ready ve image sürümü `v1.35.0`dı. Control-plane
static Pod manifesti artifact'e kopyalandı. Manifestte `--config` argümanı yoktur;
dolayısıyla bu koşu custom scheduler config'i değil, bu Kubernetes sürümünün dahili
default profile/plugin configuration'ını kullanır.

Smoke sonucu:

- `cpu-tight-train` üzerinde `24/24` Pod schedule edildi ve tamamlandı
- `unschedulableCount = 0`
- `decisionTimestampCount = 0`, beklenen instrumentation yokluğudur
- Kubernetes scheduled ve evaluator observation timestamp count'ları `24/24`tür
- `maxWorkloadOccupiedWorkerNodes = 4`
- mean dominant requested-load variance `0.00139`dur
- custom scheduler deployment canonical weights ve `2s` interval'de değişmeden
  kaldı
- postflight geçti, namespace boş kaldı ve kind container'ları durduruldu

Kubernetes condition tabanlı pending değerleri bu smoke'ta ikinci çözünürlüğünde
tamamen sıfır çıktı. Bu değer default scheduler'ın custom scheduler'dan kesin olarak
daha hızlı olduğu şeklinde yorumlanamaz; primary custom metric nanosecond-resolution
submission-to-decision latency iken external scheduler'ın decision anı instrument
edilmemiştir.

Gate 4 kararı:

- default kube-scheduler paper'da dış sistem referansı olarak yer alacak
- doğrudan ortak karşılaştırma eksenleri completion/unschedulable,
  max occupied workers ve requested-resource placement metrikleri olacak
- coarse Kubernetes pending yalnızca ayrı sütun ve açık resolution notuyla verilecek
- custom decision p95 ile default scheduler coarse pending aynı Pareto objective'ine
  konulmayacak
- official baseline için üç V2 train workload üzerinde tekrarlı koşular ve exact
  kube-scheduler image/config provenance zorunlu olacak

### 3.53. Tek komutlu tekrarlı baseline execution'ı ekledik

Official baseline'larda manuel komut tekrarı ve isim/provenance hatasını azaltmak
için evaluator CLI'ya `--repetitions` eklendi:

- commit: `4b14948 feat: add repeated baseline evaluation`
- `--repetitions 1` mevcut run isimlerini ve davranışı korur
- daha büyük değerlerde run isimleri `<name>-rNN` biçimindedir
- custom policy batch'leri repetition-outer, policy-inner round-robin sırada
  genişletilir; örneğin önce üç policy'nin `r01`, sonra `r02` koşuları gelir
- external-reference tekrarları ayrı raw run olarak çalıştırılır ve tek batch
  JSON/CSV output'unda birleştirilir
- uyumsuz workload, policy version veya scheduler mode batch'lerinin merge edilmesi
  reddedilir
- hata durumunda tamamlanmış kısmi run'lar output'a yazılabilir

Yeni orchestration full race testleri ve `go vet` altında doğrulandı. Ardından
clean `4b149481b4ec31943ce934b44437ff5fac1f5251` SHA'sından düşük maliyetli
iki-tekrarlı external-reference debug smoke yapıldı:

- results commit: `07518cd results: record repeated evaluator smoke`
- artifact directory:
  [`results/2026-08-27-v2-repetition-debug-smoke`](./results/2026-08-27-v2-repetition-debug-smoke)
- class: `debug`
- `paperEvidenceEligible: false`

Komut:

```bash
go run ./cmd/evaluator \
  --workload ../workloads/uniform-small.json \
  --scheduler-mode external-reference \
  --scheduler-name default-scheduler \
  --run-name kube-scheduler-repeat-smoke \
  --repetitions 2 \
  --poll-interval 250ms \
  --timeout 60s \
  --output /tmp/evo-v2-repetition-debug.json
```

Acceptance sonucu:

- output iki comparison row ve iki raw run içerdi
- isimler `kube-scheduler-repeat-smoke-r01/r02` olarak üretildi
- CSV tek header ve iki data row içerdi
- iki run da `4/4` completion, sıfır unschedulable ve tam Kubernetes/observer
  timestamp coverage üretti
- custom deployment değişmeden kaldı, postflight geçti ve kind durduruldu

Historical dört-Pod `uniform-small` burada yalnızca orchestration debug input'udur;
scheduler davranışı veya baseline performansı hakkında kanıt değildir. Official
Gate 4 koşuları `--repetitions 3` ve 24-Pod V2 train workload'larıyla yapılacaktır.

### 3.54. Gate 4 official tekrarlı baseline protokolünü tamamladık

Üç custom V2 baseline ve default kube-scheduler dış referansı, üç V2 train
workload üzerinde üçer tekrar çalıştırıldı. Deney, clean ve annotated
`278ab9bc1aebd341b6a37854cef7c52d3e7d35da`
(`protocol-v2-baselines-2026-08-27`) SHA'sından başlatıldı.

Kanıt zinciri:

- raw results commit: `244f405 results: record official v2 baselines`
- reusable summary tool commit: `577773f feat: add repeated baseline summarizer`
- derived analysis commit: `f1902df results: summarize official v2 baselines`
- artifact directory:
  [`results/2026-08-28-v2-official-baselines`](./results/2026-08-28-v2-official-baselines)
- run manifest: `manifest.json`
- analysis provenance: `analysis-manifest.json`
- machine-readable summary: `summary.json` ve `summary.csv`

Official protokol:

- cluster: `kind-evo-sched`, dört Ready worker, Kubernetes `v1.35.0`
- worker başına allocatable: `16000m` CPU ve `33253150720` byte memory
- workload'lar: `cpu-tight-train`, `mem-tight-train`, `fragmenting-mix-train`
- custom policy'ler: balanced, spread-heavy, consolidation-heavy
- her workload-policy çifti için üç tekrar
- custom batch sırası repetition-outer/policy-inner round-robin
- custom scheduler interval: bütün custom run'larda `250ms`
- evaluator poll interval: bütün run'larda `250ms`
- per-run timeout: `90s`
- default kube-scheduler image: `registry.k8s.io/kube-scheduler:v1.35.0`
- static Pod config checksum'u canlı manifest ile eşleşti; explicit `--config`
  verilmediği için Kubernetes dahili default profile/plugin seti kullanıldı

Acceptance sonucu:

- altı batch output'u, `36` result row ve `36` raw run üretildi
- toplam `864/864` Pod schedule edildi ve tamamlandı
- toplam `unschedulable=0`
- `27` custom run'ın tamamında `24/24` high-resolution decision timestamp vardı
- `9` external run'da decision timestamp'in bulunmaması beklenen protokol
  davranışıydı; Kubernetes scheduled ve evaluator observation coverage tamdı
- bütün input checksum'ları ve instantiate edilen mutlak Pod request'leri raw JSON
  ve manifestte saklandı
- preflight ve postflight geçti; canonical weights ile `2s` interval restore edildi
- evaluator namespace boş bırakıldı ve beş kind container'ı silinmeden durduruldu

Üç workload üzerindeki dokuz tekrar satırından türetilen aggregate özet:

| Policy | Mean p95 decision (s) | Sample SD | Mean max occupied workers | Mean load variance |
|---|---:|---:|---:|---:|
| `evok8s-v2-balanced` | 1.287 | 0.176 | 3.667 | 0.043685 |
| `evok8s-v2-spread-heavy` | 1.208 | 0.037 | 4.000 | 0.007338 |
| `evok8s-v2-consolidation-heavy` | 1.255 | 0.162 | 3.000 | 0.119646 |
| `kube-scheduler-default` | N/A | N/A | 4.000 | 0.003265 |

Bu aggregate sample SD, dokuz satırda workload'lar arası ve workload içi değişimi
birlikte taşır. Workload-regime belirsizliği yorumlanırken `summary.json` içindeki
üç tekrarlı workload-level satırlar kullanılmalıdır.

Ana gözlemler:

- spread-heavy aggregate custom decision p95'inde en düşük ortalamayı üretti
- consolidation-heavy her üç workload ve her tekrarda üç worker'da kaldı;
  spread-heavy ve default kube-scheduler dört worker kullandı
- consolidation-heavy, spread-heavy'ye göre ortalama occupied worker sayısını
  yüzde `25` düşürürken aggregate mean p95 decision latency yaklaşık yüzde `3.9`
  yükseldi
- balanced aggregate objective noktasında consolidation-heavy tarafından domine
  edildi; custom baseline Pareto uçları spread-heavy ve consolidation-heavy oldu
- `fragmenting-mix` altında spread-heavy load variance'ı yaklaşık `0.005` düzeyine
  indirirken dört worker kullandı; balanced ve consolidation-heavy yaklaşık
  `0.104-0.109` variance ile üç worker'da kaldı
- default kube-scheduler bütün workload'larda dört worker kullandı ve düşük load
  variance üretti; high-resolution decision annotation olmadığı için custom
  decision p95 ile doğrudan latency kıyasına alınmadı

Fragmenting-mix custom run'larında bazı generated Pod metadata adları 63 karakteri
aştığı için Kubernetes hostname/DNS-label uyarısı verdi. API Pod'ları kabul etti,
tam completion ve timestamp coverage sağlandı; uyarı ölçümü geçersiz kılmadı ve
official manifestte kaydedildi.

Gate 4 kararı:

- official custom baseline ve default kube-scheduler external-reference protokolü
  tamamlandı
- kube-scheduler `MostAllocated` ayrı bir opsiyonel packing referansı olarak
  kalacak; Gate 5 optimizer başlangıcını bloklamayacak
- sıradaki araştırma kapısı V2 objective'lerle official multi-seed optimization ve
  finalist doğrulamasıdır

### 3.55. Gate 5 için repo-içi official multi-seed runner ekledik

Yerel `multi-seed-experiment-runner` skill helper'ı execution sırasını doğru
kuruyordu, fakat official V2 protokolü için iki blocking eksik taşıyordu:

- optimizer'a explicit `--poll-interval 250ms` aktarmıyordu
- clean Git/tag, input checksum, cluster preflight ve seed sonrası restoration
  provenance'ını manifestte saklamıyordu

Agent session veya terminal geçmişine bağımlı kalmamak için bu helper official
koşuda doğrudan kullanılmadı. Bunun yerine Git ile sürümlenen
`scheduler/cmd/multi-seed-runner` eklendi:

- commit: `f89ffad feat: add official multi-seed runner`
- clean working tree olmadan başlamaz
- HEAD'de adı verilen annotated protocol tag yoksa başlamaz
- workload, baseline, kind config ve deployment checksum'larını run başlamadan alır
- dört Ready worker, converged rollout, canonical weights, `250ms` scheduler
  interval ve boş evaluator namespace preflight'ını hard gate olarak uygular
- seed'leri aynı cluster'ı çakıştırmadan sequential çalıştırır
- population, generation, mutation, poll ve timeout flag'lerini optimizer'a explicit
  aktarır
- manifesti ilk seed'den önce `running` yazar ve her seed sonrası atomik günceller
- her seed için JSON, Pareto CSV, all-candidates CSV ve `run.log` checksum'u tutar
- result contract, seed ve beklenen aday sayısı uyuşmadan seed'i completed saymaz
- her seed sonrası canonical weights, rollout ve namespace temizliğini doğrular
- interruption/failure halinde optimizer process group'unu sonlandırıp evaluator
  Pod'larını temizlemeyi ve canonical weights'i restore etmeyi dener

Test-first implementasyonun unit testleri, full `go test -race ./...`, `go vet ./...`
ve dirty-tree negatif acceptance testi geçti. Negatif test cluster'a erişmeden
`official run requires a clean Git tree` mesajıyla durdu.

Seçilen ilk official search bütçesi seed başına:

- üç baseline evaluation
- `12` initial individual
- `5 x 12 = 60` offspring
- toplam `75` aday
- aday başına üç train workload, toplam `225` live workload run
- yaklaşık süre seed başına `1.9-2.2` saat, üç seed için yaklaşık `6` saat

Planlanan launch komutu:

```bash
go run ./cmd/multi-seed-runner \
  --run-name 2026-08-28-v2-official-optimizer-3seed \
  --seeds 42,123,999 \
  --workloads ../workloads/v2/cpu-tight-train.json,../workloads/v2/mem-tight-train.json,../workloads/v2/fragmenting-mix-train.json \
  --baseline-file ../experiments/weights-v2.json \
  --protocol-tag protocol-v2-optimizer-2026-08-28 \
  --population 12 \
  --generations 5 \
  --mutation-rate 0.8 \
  --mutation-sigma 0.25 \
  --min-weight 0 \
  --max-weight 2 \
  --poll-interval 250ms \
  --timeout 90s
```

Bu bölüm yazılırken official optimizer henüz başlatılmadı. Sıradaki hard gate,
runner ve bu karar kaydını içeren clean HEAD'e annotated optimizer protocol tag
eklemek; ardından cluster'ı başlatıp `250ms` interval ile preflight yapmaktır.

### 3.56. Gate 5 official üç-seed search tamamlandı ve finalistler ayrıldı

Official search, clean source commit
`9bc7e5aa1642de6ea31d94f27e3ff2f4d2180ebd` ve annotated
`protocol-v2-optimizer-2026-08-28` tag'i üzerinden başlatıldı. Başlangıç
preflight'ı dört Ready worker, converged custom scheduler rollout, canonical
`0.25/0.25/0.25/0.25` V2 weights, `250ms` scheduler interval ve boş
`evo-sched-eval` namespace doğruladı.

Run manifestine göre batch `2026-08-28 00:10:37 UTC` ile
`2026-08-28 06:19:46 UTC` arasında, toplam yaklaşık `6 saat 9 dakika` çalıştı.
Seed süreleri birbirine yakın kaldı:

- `seed=42`: yaklaşık `2 saat 2 dakika 54 saniye`
- `seed=123`: yaklaşık `2 saat 3 dakika 9 saniye`
- `seed=999`: yaklaşık `2 saat 3 dakika 5 saniye`

Her seed için sözleşme eksiksiz gerçekleşti:

- `72` optimizer adayı ve `3` baseline, toplam `75` solution
- solution başına üç train workload, toplam `225` workload execution
- `5,400` Pod submission/scheduling/completion ve tam decision timestamp coverage
- sıfır unschedulable Pod
- seed sonrası canonical weights, converged rollout ve boş namespace postflight
- manifestteki bütün result/log checksum'larıyla bağımsız `sha256sum` eşleşmesi

Üç seed toplamında `216` optimizer adayı, `9` baseline evaluation, `675`
workload execution ve `16,200` gerçek Pod binding ölçüldü. Ham artefaktlar
`990d10a results: record official v2 multi-seed search` commitinde tutulmaktadır.

V1 Pareto helper'ı eski `averageP95Pending` alanını beklediği için V2 objective
şemasında çalışmadı. Sayıları elle çıkarmak yerine test-first
`scheduler/cmd/optimizer-summary` eklendi. Bu komut:

- aynı seed içindeki identical objective noktalarını deduplicate eder
- baseline'ın front'ta kalması ile optimizer'ın baseline noktasını yeniden
  keşfetmesini ayırır
- optimizer-only yeni objective noktalarını işaretler
- pending-best, node-best ve strict interior knee availability alanlarını üretir
- cross-seed exact objective equality'nin canlı-cluster timing gürültüsü nedeniyle
  stability kanıtı olmadığını output notlarında sabitler

Özetleyici `7200195 feat: add v2 multi-seed optimizer summary`, analiz artefaktları
ise `a203f50 results: summarize official v2 multi-seed search` commitlerinde
kayıtlıdır.

Per-seed combined Pareto sonuçları:

- `seed=42`: iki unique nokta
  - `init-009`: `(1.250408 s, 3.333333 worker)`, weights
    `(0.304263, 0.339953, 0.023958, 0.331826)`
  - `gen-03-007`: `(1.284070 s, 3 worker)`, weights
    `(0.021293, 0.121764, 0.458922, 0.398021)`
- `seed=123`: iki unique nokta
  - `gen-01-010`: `(1.254573 s, 4 worker)`, weights
    `(0.212481, 0.228013, 0.412212, 0.147293)`
  - `init-010`: `(1.255230 s, 3 worker)`, weights
    `(0.137785, 0.199934, 0.252638, 0.409643)`
- `seed=999`: tek unique nokta
  - `gen-03-002`: `(1.235628 s, 3 worker)`, weights
    `(0.036336, 0.349308, 0.000000, 0.614356)`

Her üç seed'in kendi tek-evaluation combined front'unda da balanced,
spread-heavy ve consolidation-heavy baseline'ların tamamı düştü; beş front
üyesinin tamamı optimizer kökenlidir. Ancak bu sonuç doğrudan "optimizer
baseline'ları geçti" şeklinde raporlanmayacaktır. Her candidate yalnızca bir kez
ölçüldüğü ve latency canlı cluster timing gürültüsü taşıdığı için bu beş policy
yalnızca **validation finalist** statüsündedir.

Hiçbir seed üç veya daha fazla unique Pareto noktası üretmedi. Bu nedenle strict
interior knee point yoktur; iki noktalı seed'lerde yalnızca uçlar, tek noktalı
seed'de ise aynı pending/node temsilcisi raporlanır.

Koşu sonunda runner weights ve namespace durumunu restore etti, fakat scheduler
interval'i deney cadence'i olan `250ms` değerinde bıraktı. Bu operasyonel açık
`e388aee feat: add scheduler cadence admin command` ile kapatıldı. Testli
`scheduler-admin` komutu canlı deployment'ı `250ms -> 2s` restore etti; ardından
beş kind container silinmeden durduruldu.

Gate 5 ara kararı:

- search tamamlandı ve beş optimizer finalisti üretildi
- yeni rejim iddiası repeated validation tamamlanana kadar provisional kalacak
- sıradaki official deney, üç baseline ile beş finalisti aynı round-robin
  protokolde en az `10` tekrar ölçmektir
- doğrulama merkezi eğilim, sample dispersion, worst-case ve yüzde `95` güven
  aralığı üretmelidir
- finalist seçimi yalnızca train objective'lerine dayanmıştır; doğrulanan
  temsilciler ayrıca `mixed-burst-holdout` üzerinde ölçülmelidir

### 3.57. Repeated finalist validation protokolünü sonuçtan önce sabitledik

Beş optimizer finalisti, üç elle seçilmiş V2 baseline ile birlikte
`experiments/weights-v2-validation-finalists.json` dosyasına optimizer
artefaktlarındaki tam canonical değerlerle aktarıldı. WALKTHROUGH'daki altı
basamaklı gösterimler deney girdisi olarak kullanılmadı. Dosya sekiz benzersiz
policy taşır ve her weight vektörünün L1 toplamı `1`dir.

Validation belirsizlik çıktısı için `baseline-summary` genişletildi. Mean, sample
standard deviation, minimum ve maximum alanlarına ek olarak iki taraflı yüzde
`95` Student-t confidence interval üretir. Tek gözlemde interval mean'e çöker;
`n > 30` için normal yaklaşım kullanılır. Aggregate satır üç workload'u havuzladığı
için paper'ın uncertainty yorumu workload-level `n=10` satırlarını esas alacaktır.

Terminal geçmişine dayalı üç ayrı evaluator komutu yerine
`scheduler/cmd/repeated-validation-runner` eklendi. Runner:

- clean working tree ve HEAD'de annotated protocol tag olmadan başlamaz
- workload, finalist weights, kind config ve deployment checksum'larını başlamadan
  manifestte saklar
- dört worker, canonical başlangıç weights, `250ms` scheduler interval, converged
  rollout ve boş evaluator namespace preflight'ını zorunlu tutar
- sekiz policy'yi repetition-major round-robin sırada çalıştırır
- her workload sonrası raw JSON/CSV yazar, tam row/Pod/timestamp acceptance uygular
  ve output checksum'larıyla manifesti atomik günceller
- başarı, hata veya interruption sonunda managed Pod'ları temizler, canonical
  weights'i ve `2s` scheduler interval'ini restore eder

Kod ve analiz değişiklikleri:

- `cb71546 feat: add confidence intervals to baseline summaries`
- `9138699 feat: add v2 validation finalist policies`
- `99d4038 feat: add official repeated validation runner`

Önceden sabitlenen train validation bütçesi:

- `8` policy
- `3` train workload
- policy-workload başına `10` tekrar
- toplam `240` workload execution
- workload başına `24` Pod, toplam `5,760` gerçek Pod binding
- primary objective'ler: mean p95 scheduling decision latency ve mean maximum
  workload-occupied worker count
- yardımcı güvenlik/yorum metrikleri: unschedulable count, load variance,
  CPU-memory imbalance, sample SD, worst-case ve yüzde `95` CI
- beklenen süre mevcut official search throughput'una göre yaklaşık `2-2.5` saat

Planlanan komut:

```bash
go run ./cmd/repeated-validation-runner \
  --run-name 2026-08-28-v2-official-finalist-validation \
  --workloads ../workloads/v2/cpu-tight-train.json,../workloads/v2/mem-tight-train.json,../workloads/v2/fragmenting-mix-train.json \
  --weights-file ../experiments/weights-v2-validation-finalists.json \
  --protocol-tag protocol-v2-finalist-validation-2026-08-28 \
  --repetitions 10 \
  --poll-interval 250ms \
  --timeout 90s \
  --expect-workers 4 \
  --expect-scheduler-interval 250ms \
  --final-scheduler-interval 2s
```

Holdout'a taşınacak temsilciler train validation sonucunu gördükten sonra keyfi
seçilmeyecektir. Önceden sabitlenen seçim kuralı:

- repeated mean objective'lerle bütün sekiz policy'nin nondominated front'unu kur
- pending-best: nondominated sette en düşük mean p95; eşitlikte daha az worker
- node-best: nondominated sette en düşük mean occupied worker; eşitlikte daha düşük
  mean p95
- en az üç unique nokta varsa, iki objective'i min-max normalize edip front
  uçlarını birleştiren doğruya en uzak interior noktayı knee adayı seç
- duplicate temsilcileri kaldır; seçilen train temsilcilerini ve üç baseline'ı
  `mixed-burst-holdout` üzerinde aynı `n=10` protokolle değerlendir

Bu bölüm yazılırken validation run henüz başlatılmadı. Sıradaki hard gate bu runner,
policy file ve karar kaydını içeren clean HEAD'e annotated validation protocol tag
eklemek; ardından cluster'ı başlatıp `250ms` preflight yapmaktır.

### 3.58. Repeated validation tek-koşu optimizer kazanımını doğrulamadı

Official validation, clean source commit
`3cfef4c40977946b406ff8778960dac8cf5d322e` ve annotated
`protocol-v2-finalist-validation-2026-08-28` tag'i üzerinden çalıştırıldı. Run
yaklaşık `2 saat 11 dakika 37 saniye` sürdü ve üç workload'un tamamında acceptance
geçti:

- `8` policy x `3` workload x `10` tekrar = `240` result row
- workload başına `1,920`, toplam `5,760` Pod
- `5,760/5,760` scheduled ve completed
- submission, scheduler decision, Kubernetes scheduled ve evaluator observation
  timestamp coverage tam
- sıfır unschedulable Pod
- final postflight dört worker, canonical weights, `2s` scheduler interval ve boş
  evaluator namespace doğruladı

Ham artefaktlar `3467045 results: record official v2 finalist validation`
commitinde tutulmaktadır. Mean, sample SD, min/max ve yüzde `95` Student-t CI
özetine repeated-mean nondominated sort ve önceden sabitlenen pending/node/knee seçimi
eklendi:

- `a7676d0 feat: add repeated mean Pareto analysis`
- `881500f results: summarize repeated finalist validation`

Repeated aggregate point estimate front yalnızca iki baseline noktasından oluştu:

- pending-best `evok8s-v2-balanced`:
  - mean p95 decision latency `1.251855 s`
  - pooled yüzde `95` CI `[1.199451, 1.304258] s`
  - mean max workload-occupied workers `3.666667`
- node-best `evok8s-v2-consolidation-heavy`:
  - mean p95 decision latency `1.264390 s`
  - pooled yüzde `95` CI `[1.205896, 1.322885] s`
  - mean max workload-occupied workers `3.000000`

Consolidation-heavy, balanced'a göre mean occupied worker sayısını yaklaşık yüzde
`18.18` azaltırken mean p95 point estimate'i yalnızca yaklaşık yüzde `1.00`, yani
`0.012536 s` artırdı. CI'lar belirgin biçimde çakıştığı için bu fark istatistiksel
üstünlük iddiası değildir; front yalnızca repeated mean point estimate'lerden
kurulmuştur.

Beş optimizer finalistinin hiçbiri repeated-mean Pareto front'ta kalmadı:

- `optimizer-s123-gen-01-010`, balanced tarafından domine edildi
- `optimizer-s123-init-010`, consolidation-heavy tarafından domine edildi
- `optimizer-s42-gen-03-007`, consolidation-heavy tarafından domine edildi
- `optimizer-s42-init-009`, consolidation-heavy tarafından domine edildi
- `optimizer-s999-gen-03-002`, consolidation-heavy tarafından domine edildi

Özellikle `optimizer-s999-gen-03-002` node-best köşesinde consolidation-heavy'ye
çok yakındı, fakat mean p95 değeri `1.265704 s` ile baseline'ın `1.264390 s`
değerinden yaklaşık `0.001313 s` yüksekti. Bu büyüklük canlı-cluster timing
gürültüsünden güvenle ayrılamaz.

Araştırma kararı:

- single-evaluation search'te görülen optimizer-only kazanım tekrarlı protokolde
  yeniden üretilemedi
- bu sonuç başarısız artefakt değil, tek ölçümlü expensive black-box search'ün
  gürültüyü optimize edebileceğini gösteren geçerli negatif doğrulamadır
- "optimizer baseline'ları geçti" claim'i paper ve CFP'den çıkarılacaktır
- optimizer'ın katkısı, canlı-cluster policy search metodolojisi ve measurement
  noise dersi olarak yeniden çerçevelenecektir
- iki unique nokta bulunduğu için strict interior knee yine yoktur

Önceden sabitlenen holdout kuralı repeated train front temsilcilerini üç baseline ile
birleştirip duplicate'leri kaldırıyordu. Front temsilcileri zaten balanced ve
consolidation-heavy olduğundan holdout policy seti mevcut
`experiments/weights-v2.json` içindeki üç baseline'a çöker. Sıradaki official run,
`mixed-burst-holdout` üzerinde üç policy x `10` tekrar, toplam `30` execution ve
`720` Pod olacaktır.

Önceden sabitlenen holdout komutu:

```bash
go run ./cmd/repeated-validation-runner \
  --run-name 2026-08-29-v2-official-holdout-baselines \
  --workloads ../workloads/v2/mixed-burst-holdout.json \
  --weights-file ../experiments/weights-v2.json \
  --protocol-tag protocol-v2-holdout-2026-08-29 \
  --repetitions 10 \
  --poll-interval 250ms \
  --timeout 90s \
  --expect-workers 4 \
  --expect-scheduler-interval 250ms \
  --final-scheduler-interval 2s
```

Bu komut çalıştırılmadan önce clean HEAD/tag ve live preflight yeniden
doğrulanacaktır. Holdout sonucu train policy seçimini değiştirmek için
kullanılmayacak; yalnızca seçilmiş baseline tradeoff'unun farklı burst yapısında
korunup korunmadığını değerlendirecektir.

### 3.59. Official holdout üç noktalı baseline tradeoff'unu doğruladı

Önceden sabitlenen holdout, clean source commit
`9de2178716eb11f4ec0df10faa3fb599ac251d50` ve annotated
`protocol-v2-holdout-2026-08-29` tag'i üzerinden çalıştırıldı. Run yaklaşık
`16 dakika 50 saniye` sürdü ve acceptance koşullarının tamamını geçti:

- `3` baseline x `10` tekrar = `30` result row
- tekrar başına `24`, toplam `720` Pod
- `720/720` scheduled ve completed
- scheduler decision, Kubernetes scheduled ve evaluator observation timestamp
  coverage ayrı ayrı `720/720`
- sıfır unschedulable Pod
- input ve output checksum'larının tamamı manifest ile bağımsız olarak eşleşti
- postflight dört worker, canonical `0.25/0.25/0.25/0.25` weights, `2s`
  scheduler interval ve boş evaluator namespace doğruladı

Kanıt zinciri ham sonuç ile türetilmiş analizi ayrı commitlerde tutar:

- `59dc049 results: record official v2 holdout baselines`
- `937bd79 results: summarize official v2 holdout`
- artifact directory:
  [`results/2026-08-29-v2-official-holdout-baselines`](./results/2026-08-29-v2-official-holdout-baselines)

`baseline-summary` çıktısı aynı input'tan ikinci kez üretilip byte-for-byte
karşılaştırıldı; JSON ve CSV deterministik olarak yeniden üretildi. Holdout
repeated-mean point estimate front'u üç benzersiz nondominated noktadan oluştu:

| Rol | Policy | Mean p95 decision (s) | Yüzde 95 CI (s) | Mean max occupied workers | Mean dominant-load variance |
| --- | --- | ---: | ---: | ---: | ---: |
| pending-best | `evok8s-v2-spread-heavy` | 1.252656 | [1.156833, 1.348480] | 4 | 0.004271 |
| knee | `evok8s-v2-balanced` | 1.260228 | [1.171981, 1.348475] | 3 | 0.070861 |
| node-best | `evok8s-v2-consolidation-heavy` | 1.304153 | [1.183966, 1.424340] | 2 | 0.165034 |

Point estimate tradeoff'u:

- balanced, spread-heavy'ye göre occupied worker sayısını yüzde `25` azaltırken
  mean p95 yalnızca yüzde `0.60` arttı
- consolidation-heavy, balanced'a göre occupied worker sayısını yüzde `33.33`
  azaltırken mean p95 yüzde `3.49` arttı
- consolidation-heavy, spread-heavy'ye göre worker sayısını yarıya indirirken
  mean p95 yüzde `4.11` arttı
- üç unique nokta bulunduğu için normalize edilmiş doğru-uzaklığı kuralı gerçek
  bir interior knee seçebildi; bu temsilci `evok8s-v2-balanced` oldu

Latency CI'ları belirgin biçimde çakışmaktadır. Bu nedenle p95 sıralaması
istatistiksel üstünlük claim'i değil, descriptive point estimate tradeoff'udur.
Buna karşılık occupied-worker sayısı her tekrarda sırasıyla `4`, `3` ve `2` oldu.
Requested-load variance da spread'den consolidation'a doğru belirgin arttı; bu
yardımcı metrik policy karakterinin beklenen yayma/sıkıştırma yönünde değiştiğini
açıklar, gerçek donanım utilization'ı olarak yorumlanmaz.

Araştırma kararı:

- bağımsız holdout, üç elle tanımlı policy arasında latency-consolidation
  tradeoff'unu ve dengeli bir knee noktasını doğruladı
- train repeated front iki, holdout front üç unique noktadan oluştu; Pareto şekli
  workload'a duyarlıdır ve evrensel policy sıralaması iddia edilmeyecektir
- önceden sabitlenen seçim kuralı optimizer finalistlerini holdout'a taşımadığı için bu
  koşu optimizer genellemesini test etmez
- repeated train validation'ın negatif optimizer sonucu değişmez: bu kanıt seti
  optimizer'ın baseline'ı geçtiği iddiasını desteklemez
- paper/CFP anlatısında doğrulanmış ana sonuç, canlı cluster'da workload-duyarlı
  baseline tradeoff'u; yöntem dersi ise single-evaluation search'ün ölçüm
  gürültüsüne duyarlılığı olacaktır

Gate 5 tamamlandı. Canlı sistem canonical postflight sonrasında kapatıldı ve beş
kind container resource tasarrufu için durduruldu. Sıradaki aktif kapı, yalnızca
commitlenmiş official kanıtları kullanan Gate 6 paper/repo/CFP revizyonudur.

### 3.60. Gate 6 paper'ı official V2 kanıtıyla yeniden kurduk

Gate 6'ya doğrudan metin yazarak değil, önce claim--artifact audit'i yaparak
başlandı. İlk audit
[`paper/evidence-audit-v2.json`](./paper/evidence-audit-v2.json) içinde `3`
blocking, `6` major ve `1` minor bulgu kaydetti; bu başlangıç durumu
`aab8284 docs: audit paper against official v2 evidence` commit'iyle
donduruldu. Audit'in kullandığı tek final kanıt sınıfı aşağıdaki dört official
artifact zinciridir:

- [`results/2026-08-28-v2-official-baselines`](./results/2026-08-28-v2-official-baselines)
- [`results/2026-08-28-v2-official-optimizer-3seed`](./results/2026-08-28-v2-official-optimizer-3seed)
- [`results/2026-08-28-v2-official-finalist-validation`](./results/2026-08-28-v2-official-finalist-validation)
- [`results/2026-08-29-v2-official-holdout-baselines`](./results/2026-08-29-v2-official-holdout-baselines)

Paper'ın ana araştırma anlatısı bu audit sonucuna göre değiştirildi:

- V1 `FreeCPU/FreeMem/PodPenalty/Packing` yüzeyi ve seed-42 pilot grafikleri
  historical evidence olarak bırakıldı; final paper'dan çıkarıldı
- yöntem, nonnegative L1-normalized `FreeCPU`, `FreeMem`, `Balance` ve
  `Consolidation` V2 policy contract'ı üzerinden yeniden yazıldı
- üç seed'deki single-evaluation Pareto noktaları yalnızca provisional search
  sonucu olarak raporlandı
- beş optimizer finalistinin `n=10` repeated validation'da baseline'lar
  tarafından domine edilmesi ana negatif metodoloji sonucu yapıldı
- bağımsız holdout'taki spread/balanced/consolidation `4/3/2` occupied-worker
  front'u doğrulanmış policy tradeoff'u olarak ayrıldı
- default kube-scheduler yalnızca completion, placement ve requested-load
  ölçümleri için external reference olarak tutuldu; eksik custom decision
  timestamp'i nedeniyle latency baseline'ı yapılmadı
- çakışan latency confidence interval'ları açıkça yazıldı ve istatistiksel
  üstünlük iddiası kaldırıldı
- title, offline/live search'ü online workload adaptation gibi göstermemek için
  `Live Multi-Objective Policy Search for a Parameterized Kubernetes Scheduler`
  olarak değiştirildi

Sonuç görselleri de official V2 summary değerlerinden yeniden üretildi:

- [`paper/figures/baseline-placement.tex`](./paper/figures/baseline-placement.tex):
  üç train workload için custom baseline ve default kube-scheduler occupied-worker
  karşılaştırması
- [`paper/figures/repeated-validation.tex`](./paper/figures/repeated-validation.tex):
  beş optimizer finalisti ile üç baseline'ın repeated-mean dominance görünümü
- [`paper/figures/holdout-pareto.tex`](./paper/figures/holdout-pareto.tex):
  yüzde 95 Student-t latency interval'lı üç noktalı holdout front'u ve balanced
  knee

Scheduler decision-flow ve live experiment-loop TikZ figürleri V2 terimleriyle
güncellendi. Related-work bölümü Borg, Omega, Firmament, DRF, Tetris, NSGA-II,
noisy evolutionary optimization ve official Kubernetes/kind kaynaklarıyla
genişletildi; discovery metadata'sı yerine primary paper ve official docs
bilgileri kullanıldı.

Paper değişiklikleri atomik olarak
`549e272 docs: rewrite paper around official v2 evidence` commit'ine alındı.
Ardından evidence audit tarihsel başlangıç bulgularını silmeden closure kaydıyla
güncellendi ve `9b678f0 docs: close official v2 paper evidence audit` commit'i
oluşturuldu. Closure audit `10/10` bulguyu resolved kabul eder ve paper commit
SHA'sını doğrudan taşır.

Final paper doğrulamaları:

- `latexmk -pdf -interaction=nonstopmode -halt-on-error main.tex` geçti
- yerel build çıktısı `paper/main.pdf`, IEEE conference US Letter formatında
  `6` sayfa üretildi; PDF build ürünü olarak Git tarafından ignore edilmektedir
- undefined citation/reference, LaTeX error ve overfull box sayısı sıfır
- `qpdf --check` PDF yapısını hatasız doğruladı
- bütün fontlar embedded Type 1 olarak doğrulandı
- altı sayfanın tamamı `150 dpi` render edilip clipping, overlap, broken figure,
  tablo ve eksen okunabilirliği açısından görsel incelendi
- son sayfadaki kaynakça `\IEEEtriggeratref{6}` ile iki sütuna dengelendi
- abstract, tablolar, yüzdeler ve figür koordinatları dört official summary JSON
  artifact'iyle yeniden karşılaştırıldı

Araştırma kararı:

- Gate 6'nın paper-evidence alt-fazı tamamlandı ve submission claim seti official
  V2 kanıtına kapatıldı
- paper artık optimizer üstünlüğü değil, canlı single-evaluation fitness'in
  gürültüye duyarlılığı ile workload-dependent latency--consolidation tradeoff'unu
  savunmaktadır
- yeni bir deney sonucu paper claim'ini değiştirecekse yeni versioned artifact,
  ayrı results/analysis commit'i ve yeni evidence-audit closure gerekecektir
- Gate 6'nın kalan işleri global README/reproduction yüzeyi ve KubeCon CFP
  metninin aynı commitlenmiş kanıt setine bağlanmasıdır

### 3.61. Repo giriş ve tek komutlu evidence verification yüzeyini ekledik

Paper evidence closure sonrasında dışarıdan gelen bir kullanıcının projeyi yalnızca
bu uzun ledger üzerinden çözmek zorunda kalmaması için public repo yüzeyi kuruldu.
İngilizce [`README.md`](./README.md) şu ayrımları açık biçimde yapar:

- sistem artefact'i, V2 policy contract'ı ve live evaluator mimarisi
- official baseline, multi-seed search, repeated validation ve holdout sonuçları
- optimizer'ın one-shot başarısı ile repeated validation negatif sonucunun ayrımı
- default kube-scheduler'ın placement-only external-reference sınırı
- hızlı offline verification, functional live smoke ve pahalı official protocol
  reproduction akışlarının birbirinden ayrılması
- kind, synthetic workload, proxy metric ve timestamp-source sınırlılıkları

Test edilen Kubernetes `v1.35.0`, kind `v0.31.0`, Go `1.25`, client-go `v0.35.0`,
Docker `29.7.2` ve BusyBox `1.36.1` sürümleri README'de official manifestlerle
uyumlu biçimde sabitlendi. README'deki Mermaid şeması workload, evaluator,
scheduler deployment, cluster, artifacts ve optimizer objective feedback
döngüsünü GitHub üzerinde ek build gerektirmeden görünür kılar.

Repo için Apache License 2.0 seçildi ve [`LICENSE`](./LICENSE) eklendi. Seçim,
Kubernetes ekosistemiyle uyumlu permissive kullanım ve açık patent lisansı sağlamak
için yapıldı; copyright sahibi `Ismail Tarim`, yıl `2026` olarak kaydedildi.

Tek komutlu doğrulama yüzeyi
[`scripts/verify-official-v2.sh`](./scripts/verify-official-v2.sh) ve kök
[`Makefile`](./Makefile) ile oluşturuldu. `make verify` canlı cluster başlatmadan:

1. bütün Go paketlerinde `go test -count=1 ./...` çalıştırır
2. `go vet ./...` çalıştırır
3. dört official kanıt zincirinin summary çıktılarını yeniden üretir
4. regenerated analizleri committed sonuçlarla karşılaştırır
5. evidence audit closure ve audited paper commit bağını doğrular
6. paper'ı yeniden derleyip sayfa sayısı, LaTeX log'u, PDF yapısı ve Type 1
   fontları kontrol eder

Verifier geliştirilirken ilk byte comparison eski official baseline summary'de
fark gösterdi. İnceleme bunun sayısal drift olmadığını ortaya koydu:

- official baseline summary, `baseline-summary` aracına confidence interval ve
  repeated Pareto alanları eklenmeden önce üretilmişti
- mevcut araç eski bütün alanlarda aynı değerleri üretiyor, yalnızca additive CI,
  Pareto ve schema-version alanlarını ekliyordu
- verifier bu bilinen schema evolution'ını yalnızca yeni alanları çıkararak
  normalize eder ve eski bütün alanları karşılaştırır
- optimizer summary'de yalnızca nondeterministic `generatedAt` alanı dışlanır;
  geri kalan JSON ve Pareto CSV karşılaştırılır
- repeated finalist validation ve holdout JSON/CSV çıktıları byte-for-byte
  karşılaştırılır

Bu ayrım, yeniden analiz aracının evrilmesini raw evidence drift'i gibi yanlış
raporlamadan ortak sayısal sözleşmeyi sıkı biçimde doğrular. `make verify` uçtan
uca geçti. Shell syntax `bash -n` ile doğrulandı; yerel ortamda `shellcheck`
bulunmadığı için ayrıca shellcheck raporu üretilmedi.

Makefile ayrıca şu güvenli operasyonel entrypoint'leri sağlar:

- `make cluster-up`: durdurulmuş `evo-sched` container'larını başlatır veya pinned
  image ile yeni 4-worker cluster kurar
- `make scheduler-image` ve `make scheduler-deploy`: image build/load ile RBAC ve
  strict rollout deployment'ını uygular
- `make live-smoke`: V2 baseline'larla küçük wiring smoke çalıştırır; bu çıktı
  functional check'tir ve paper evidence değildir
- `make cluster-stop`: cluster state'ini silmeden beş kind container'ını durdurur
- `make test`, `make vet` ve `make paper`: alt doğrulamaları ayrı çalıştırır

Değişiklikler iki atomik commit'e ayrıldı:

- `8d4dc5a chore: add official v2 verification entrypoint`
- `563705b docs: add public project overview and license`

Gate 6'nın repo giriş, lisans, pinned-version ve reproduction alt-fazı tamamlandı.
Aktif kalan iş, aynı conservative claim setinden KubeCon CFP başlık, abstract,
audience outcome ve session-outline metnini üretmektir.

### 3.62. KubeCon Europe 2027 CFP anlatısını official V2 kanıtına bağladık

CFP taslağı yazılmadan önce 29 Ağustos 2026 tarihinde güncel resmi kaynaklar
yeniden kontrol edildi:

- [KubeCon + CloudNativeCon Europe 2027 CFP](https://events.linuxfoundation.org/kubecon-cloudnativecon-europe/program/cfp/)
- [Submission Reviewer Guidelines](https://events.linuxfoundation.org/kubecon-cloudnativecon-europe/program/submission-reviewer-guidelines/)
- [Sessionize submission portal](https://sessionize.com/kubecon-cloudnativecon-europe-2027/)

Resmi takvimde CFP kapanışı `11 Ekim 2026 23:59 CEST`, bildirim tarihi
`7 Aralık 2026`, program ilanı `9 Aralık 2026` ve etkinlik tarihleri
`15-18 Mart 2027` olarak doğrulandı. Resmi formatlar arasında 30 dakikalık
`Session Presentation` ve dijital `Poster Session` bulunuyor. `Emerging +
Advanced` track'i cloud-native araştırma, akademik paper, proof-of-concept ve
ileri/niche teknik konuları açıkça kabul ediyor. Reviewer yönergesi içerik,
özgünlük, relevance ve speaker yeterliliğini; breakout oturumlarında ayrıca hedef
kitle uyumu, yazım kalitesi ve advertorial olmamayı değerlendiriyor.

Bu bilgilerle şu positioning kararı sabitlendi:

- primary track: `Emerging + Advanced`
- alternate track: `Operations + Performance`
- primary format: 30 dakikalık `Session Presentation`
- fallback format: `Poster Session`
- experience level taslağı: `Intermediate`; Sessionize seçenekleri kullanıcı
  tarafından son kez görülmeden nihai kabul edilmeyecek
- case study: `false`; çalışma bir organizasyonun production implementation
  raporu değil, gerçek Kubernetes control-plane yolu üzerinde tekrarlanabilir
  experimental-systems araştırmasıdır

Poster araştırma ve novel-solution işleri için resmi olarak önerilen güvenli bir
format olsa da primary format yapılmadı. Çalışmanın practitioner değeri yalnızca
Pareto figürünü göstermekten daha geniştir: canlı bir optimizer'ın measurement
noise'u optimize edebilmesi, policy-space identifiability, timestamp-source
ayrımı, hard validity gate'leri ve repeated validation birlikte 30 dakikalık
teknik bir failure-analysis anlatısını destekliyor.

Başlık şu şekilde seçildi:

> Searching the Kubernetes Scheduler Policy Space: Lessons From Noisy Live Optimization

CFP'nin ana sorusu paper'ın sonuç cümlesinden practitioner diline çevrildi:

> When an optimizer reports a better Kubernetes scheduling policy, did it
> improve placement or learn the timing noise of the control plane?

Anlatı şu sırayı izler:

1. gerçek custom scheduler, rollout, workload replay ve Pod binding döngüsü
2. V1'deki scale-equivalent/redundant policy-space problemi ve V2 onarımı
3. scheduler decision timestamp, Kubernetes time ve evaluator observation lag
   kaynaklarının ayrıştırılması
4. üç seed'in ilk bakışta başarılı görünen optimizer-only Pareto sonuçları
5. beş finalistin `n=10` repeated validation'da nondominated kalamaması
6. unseen holdout üzerinde `4/3/2` occupied-worker baseline tradeoff'u ve
   çakışan latency confidence interval'ları
7. live control-loop tuning için tekrar kullanılabilir deney checklist'i

Bu seçimle özellikle şu güçlü ama desteklenmeyen iddialar reddedildi:

- evolutionary search Kubernetes default scheduler'ını geçti
- optimizer finalistleri workload'lar dışında genellendi
- kind sonuçları production performansını kanıtladı
- workload-occupied worker sayısı powered-node maliyetine eşittir
- holdout latency ortalamaları istatistiksel olarak birbirinden farklıdır

Versioned taslak
[`cfp/kubecon-europe-2027.json`](./cfp/kubecon-europe-2027.json) içinde tutulur.
Dosya yalnızca form metnini değil, track/format gerekçesini, 30 dakikalık akışı,
supported/excluded claim setini, ilgili open-source projeleri, konuşmacı bilgisini
ve submission checklist'ini de taşır. JSON `jq empty` ile doğrulandı. Session
description `179`, ecosystem benefit metni `89` kelimedir. CFP artefact'i paper ve
README ile aynı official V2 claim setine bağlandı ve ayrı commitlendi:

- `b15d964 docs: draft KubeCon Europe 2027 proposal`

Submission öncesinde insan veya dış platform gerektiren işler kasıtlı olarak açık
bırakıldı:

- son bir yıl içinde benzer bir Linux Foundation/CNCF konuşması yapılıp
  yapılmadığı kullanıcı tarafından teyit edilecek
- speaker bio ve Sessionize experience level kullanıcı tarafından onaylanacak
- önceki konuşma kaydı yoksa birkaç dakikalık YouTube speaking sample üretilecek
- tagged GitHub release veya preprint üzerinden stabil public paper PDF URL'si
  sağlanacak
- submission anında Sessionize alan ve limitleri yeniden kontrol edilecek

Bu nedenle Gate 6'nın repo-içi CFP drafting alt-fazı tamamlandı; başvurunun kendisi
henüz gönderilmiş sayılmıyor. Sıradaki kapı, dış submission artefact'lerini
tamamlayıp Sessionize formunu kullanıcı onayıyla doldurmaktır.

### 3.63. Research release, literature ve noisy-optimization skill yüzeyini güçlendirdik

V1 ile V2'yi karşılaştıran bağımsız bir değerlendirme, V2'nin merkezi negatif
sonucunu doğru okurken üç tekrar eden agent-workflow sürtünmesini görünür hale
getirdi:

- local checkout'ta tamamlanmış kanıt ile public remote/release durumunun
  karıştırılması
- en yakın Kubernetes + multi-objective optimization çalışmalarının genel
  related work içinden güvenilir publication metadata'sıyla ayrılması
- point-estimate Pareto dominance, statistical rejection, pre-specified protocol
  ve public preregistration kavramlarının aynı dilde kullanılabilmesi

Bu boşluklar için `~/.codex/skills` altında üç otomatik keşfedilebilir skill
oluşturuldu:

- `research-artifact-release`: local, committed, public ve released durumlarını
  ayrı denetler; branch/tag/PDF/raw evidence/checksum/release zincirini doğrular
  ve push/release gibi dış mutation'ları explicit kullanıcı onayına bağlar
- `nearest-neighbor-literature-audit`: novelty claim'ini yöntem bileşenlerine
  ayırır, aggregator'ları yalnızca discovery için kullanır, primary/canonical
  kaynaktan DOI/venue/publication status doğrular ve defensible novelty matrix'i
  üretir
- `noisy-live-optimization-protocol`: live fitness için estimand, noise source,
  p95 sample support, budget accounting, resampling/promotion/stopping rule ve
  point-estimate Pareto yorumunu official koşudan önce sabitler

Mevcut research skill'leri de yalnızca bu çalışmada gözlenen somut hatalara göre
audit edildi. Beş dar güncelleme yapıldı:

- `research-experiment-ledger`: local/committed/public/released ayrımı ve local
  annotated tag'in tek başına public preregistration olmadığı eklendi
- `paper-evidence-audit`: küçük örneklem tail percentile kontrolü,
  point-estimate Pareto dili, `rejected/significant/preregistered` terminoloji
  gate'leri ve public availability doğrulaması eklendi
- `policy-space-audit`: optimizer'ın normalize edilmiş phenotype'i population,
  crossover, mutation ve dedup boyunca canonical saklayıp saklamadığı denetime
  eklendi
- `multi-seed-experiment-runner`: repo-içi provenance-gated runner varsa ona
  öncelik verildi; generic helper output'unun tek başına official evidence
  sayılamayacağı açıklandı
- `pareto-experiment-analysis`: V1 ve V2 objective schema ayrımı, one-shot
  sonuçların provisional statüsü ve repeated-mean point-estimate dili eklendi

Audit sırasında `pareto-experiment-analysis/scripts/pareto_summary.py` helper'ının
V2 `result.json` üzerinde eski `averageP95Pending` alanını bekleyip `KeyError` ile
çöktüğü yeniden üretildi. Helper iki objective contract'ını otomatik tanıyacak
şekilde güncellendi:

- V1: `averageP95Pending`, `averageMaxActiveNodes`
- V2: `averageP95SchedulingDecisionLatencySeconds`,
  `averageMaxWorkloadOccupiedWorkerNodes`

Güncellenmiş helper V1 seed-42 artifact'inde eski sayıları korudu; official V2
seed-42 artifact'inde `72` optimizer adayı, `3` baseline, `2` combined Pareto
üyesi ve `2` unique objective point üretti. Üç yeni ve beş güncel skill kurulu
konumlarında `quick_validate.py` kontrolünden geçti. Repo dışındaki global skill
dosyaları Git'e eklenmedi; bu bölüm karar ve doğrulama provenance'ını repo içinde
korur.

Bu bakım turundan sonra sıradaki research gate değişmedi. Önce paper terminoloji,
small-sample p95 limitation ve nearest-neighbor literature düzeltmeleri yapılacak;
ardından V2 public artifact release hazırlanacak. Noise-aware V3 extension ancak
ayrı, budget-matched ve sonuçtan önce sabitlenmiş protokolle açılacaktır.

### 3.64. Paper claim dilini ve en yakın literatür konumlandırmasını kapattık

V2 paper bağımsız tekrar değerlendirmesinde bilimsel sonucu değiştirmeyen, fakat
claim gücünü ve özgünlük sınırını etkileyen iki risk bulundu:

- Pareto dominance sonucunun `rejected` veya `survived` gibi istatistiksel test
  çağrışımı yapan kelimelerle özetlenmesi
- Kubernetes scheduling'de NSGA-II kullanımının kendisinin yenilik gibi
  algılanabilmesi

İlk risk için paper'da şu terminoloji sözleşmesi uygulandı:

- ana repeated-validation sonucu `none of the five finalists remained
  nondominated under repeated-mean evaluation` olarak yazıldı
- public preregistration kanıtı bulunmadığı için `preregistered` yerine
  `pre-specified and committed before execution` kullanıldı
- holdout front her yerde `point-estimate Pareto front` olarak adlandırıldı;
  Pareto dominance'ın significance test olmadığı korunmaya devam etti
- her run'ın yalnızca `24` Pod içermesi nedeniyle linear-interpolation p95'in 22.
  ve 23. sıralı latency gözlemleri arasında kaldığı ve birkaç upper-tail örneğe
  duyarlı olduğu limitation'a eklendi

İkinci risk için nearest-neighbor audit discovery sonucu değil primary/canonical
kaynaklar üzerinden kapatıldı:

- Di Cicco vd., Kubernetes replica scheduling ve resource allocation'ı
  heterogeneous multi-cluster düzeyinde MO-ILP olarak kurup NSGA-II, NSGA-III ve
  multi-swarm PSO ile latency, cost ve interruption objective'lerini araştırır:
  <https://doi.org/10.23919/CNSM62983.2024.10814307>
- Gouaouri vd., TOPSIS ile centralized/distributed NSGA-II'yi online
  power-aware Kubernetes scheduler plugin'ine bağlayıp power, utilization ve
  load balancing objective'lerini optimize eder:
  <https://doi.org/10.1109/TNSM.2025.3630045>
- Branke, noisy multi-objective estimate'lerin decision-maker selection error
  üretebildiğini ve conventional performance metric'lerin bunu kaçırabildiğini
  gösterir:
  <https://doi.org/10.1109/TEVC.2024.3438115>

2026 KU-NSGA-II çalışması yalnızca under-review preprint olarak bulunabildiği ve
altı sayfalık paper'da yukarıdaki peer-reviewed en yakın çalışmalar yeterli
konumlandırmayı sağladığı için bibliography'ye alınmadı. Bu bir "literatürde yok"
iddiası değildir; publication status ve sayfa bütçesine dayalı inclusion kararıdır.

Paper'ın savunulabilir özgünlük ekseni şu şekilde daraltıldı:

> Existing work primarily asks whether multi-objective or evolutionary
> scheduling can improve placement objectives. This study instead examines
> whether apparent improvements remain valid when fitness is obtained from
> noisy, repeated live-cluster measurements.

Bibliography yeni üç kaynakla `15` girdiye çıktı, ilk citation sırasına göre
yeniden düzenlendi ve `IEEEtriggeratref{2}` ile son sayfa sütunları dengelendi.
Paper altı sayfada kaldı; tüm fontlar embedded Type 1, `qpdf --check` temiz ve
undefined citation/reference veya overfull box yoktur.

Bu faz üç atomik commit ile kapatıldı:

- `bc1f5c4 docs: tighten paper claim language`
- `b9636e1 docs: position paper against nearest work`
- `0f2fd52 docs: refresh v2 paper evidence closure`

Son commit'teki [evidence audit](paper/evidence-audit-v2.json), closure paper
SHA'sını `b9636e1f31a5c5dbf755d0e26aa51fd11a4c59b9` olarak sabitler ve 24-Pod
p95 riskini residual limitation olarak taşır. Ardından `make verify`; Go test,
`go vet`, dört official analysis ailesinin regeneration/comparison'ı,
paper--audit SHA kontrolü, LaTeX build, `qpdf`, sayfa ve font kontrollerinin
tamamında geçti.

Bu kapanıştan sonra local/committed V2 evidence hazırdır; public/released durumu
henüz iddia edilmemektedir. Sıradaki aktif iş `research-artifact-release` audit'i
ile branch/tag/remote farkını çıkarmak, sonra kullanıcı onayıyla stabil V2 release
ve public paper PDF oluşturmaktır. Killer figure bu public evidence setinden sonra
üretilecek; V3 bu iki işi bloklamayacaktır.

### 3.65. Public V2 release paketini doğrulanabilir ve mutasyonsuz hazırladık

Paper ve official evidence local olarak kapanmış olsa da bunun GitHub'da public
ve stabil bir release olduğu varsayılmadı. `research-artifact-release` audit'i
local, committed, public ve released durumlarını ayrı ayrı kontrol etti.

Audit anındaki ref durumu şuydu:

- local branch `research/policy-space-v2`, clean worktree ve HEAD `32f8baa`
- local protocol tag'leri:
  - `protocol-v2-baselines-2026-08-27` -> `278ab9bc`
  - `protocol-v2-optimizer-2026-08-28` -> `9bc7e5aa`
  - `protocol-v2-finalist-validation-2026-08-28` -> `3cfef4c4`
  - `protocol-v2-holdout-2026-08-29` -> `9de21787`
- GitHub repository public durumdaydı, ancak remote üzerinde yalnızca eski
  `main` branch'i vardı; V2 branch, protocol tag veya release bulunmuyordu

Dolayısıyla o anda durum matrisi şuydu:

| Durum | Sonuç | Kanıt |
| --- | --- | --- |
| Local complete | Evet | Dört official evidence ailesi ve paper `make verify` geçiyor |
| Committed | Evet | Evidence, audit ve paper source V2 branch geçmişinde erişilebilir |
| Public | Hayır | Remote V2 branch ve protocol tag'leri yok |
| Released | Hayır | GitHub release ve stabil PDF asset'i yok |

Release işleminin elle seçilmiş dosyalara veya kirli worktree'ye bağlı kalmaması
için `32f8baa chore: add reproducible v2 release packaging` commit'i oluşturuldu.
Bu commit:

- `scripts/package-v2-release.sh` ile clean worktree ve doğru branch'i zorunlu
  tutar
- dört protocol tag'in HEAD ancestor'ı olduğunu doğrular
- yeniden `make verify` çalıştırır
- generated `paper/main.pdf` dosyasını release PDF asset'ine kopyalar
- yalnızca dört official result dizini ve `paper/evidence-audit-v2.json` için
  Git-tracked archive üretir
- branch, HEAD, protocol tag target'ları, evidence source'ları, paper source SHA,
  known limitations, boyut ve SHA-256 değerlerini JSON manifestte saklar
- `SHA256SUMS`, JSON contract ve tar archive integrity kontrollerini çalıştırır

`make release-package RELEASE_OUTPUT=/tmp/evo-sched-v2-release-32f8baa`
komutu clean `32f8baa` checkout'unda çalıştırıldı. Tam `make verify` tekrar geçti
ve yerel paket şu asset'lerle üretildi:

| Asset | Boyut | SHA-256 |
| --- | ---: | --- |
| `evo-sched-v2-paper.pdf` | 249468 byte | `915b405a999c481625d35cbf777e387a17c10e042aca2a37245cecfbca8f90ba` |
| `evo-sched-v2-official-evidence.tar.gz` | 2621821 byte | `bed81af4105151309f913f4e61ca9da0af8e3653306fe746633dd5745e79496e` |
| `evo-sched-v2-release-manifest.json` | generated manifest | `6f6f863c6175d4b3e6bf367c3fa94ad3992ff682e4570b9cb6420dbd9436fa91` |

Paketin intended tag'i `v2-evidence-2026-08-31` olarak kaydedildi. Bu işlem
yalnızca local release preparation'dır; push, tag publication, GitHub release
creation veya asset upload yapılmadı. README'ye aynı mutasyonsuz paketleme akışı
ve dört asset sözleşmesi eklendi.

Sıradaki kapı, bu dokümantasyon commit'inden sonra final clean HEAD üzerinden
paketi yeniden üretmektir. Ardından branch, dört protocol tag, annotated release
tag ve GitHub release asset'leri için dış mutation komutları kullanıcıya açıkça
gösterilecek ve çalıştırılmadan hemen önce onay alınacaktır. Public URL'ler ancak
push/upload sonrası bağımsız doğrulama geçerse paper/CFP kaynağı olarak kabul
edilecektir.

### 3.66. V2 evidence release'i private GitHub reposunda yayımladık

Kullanıcı external mutation kapısını onayladı, ancak çalışma henüz genel erişime
açılmasın diye repository'nin şimdilik private tutulmasını istedi. Önce GitHub
repository görünürlüğü `PRIVATE` yapıldı ve API üzerinden doğrulandı. Bu kararın
sonucu olarak release stabil ve uzaktan doğrulanabilir durumdadır, fakat giriş
gerektirdiği için CFP'de public Additional Resource olarak henüz kullanılamaz.

Private remote'a şu ref'ler gönderildi:

- `research/policy-space-v2` -> `84c9e8050549b4159fd744a36434fe24b1d3a2d5`
- dört annotated protocol tag ve 3.65'te kaydedilen tarihsel commit target'ları
- annotated release tag `v2-evidence-2026-08-31` -> `84c9e8050549b4159fd744a36434fe24b1d3a2d5`

Ardından `EvoSched V2 Official Evidence` adlı non-draft, non-prerelease GitHub
release oluşturuldu:

<https://github.com/ismailtrm/evo-sched/releases/tag/v2-evidence-2026-08-31>

Release'e final clean `84c9e80` paketinden şu dört asset yüklendi:

- `evo-sched-v2-paper.pdf`
- `evo-sched-v2-official-evidence.tar.gz`
- `evo-sched-v2-release-manifest.json`
- `SHA256SUMS`

Yayın sonrası doğrulama yalnızca create komutunun başarı koduna dayanmadı:

- `git ls-remote` ile branch ve beş annotated tag remote'dan tekrar okundu
- annotated tag'lerin dereference edilmiş commit target'ları local provenance ile
  eşleşti
- `gh release view` dört asset'in `uploaded` durumunu, size ve SHA-256 digest'ini
  doğruladı
- dört asset yeni bir `/tmp` dizinine GitHub'dan tekrar indirildi
- indirilen paper, evidence archive ve manifest için release içindeki
  `SHA256SUMS` doğrulaması geçti
- indirilen `SHA256SUMS` dosyasının kendi digest'i
  `a7d50639519b28506789e595528da354c28bbabfcb1145b0f4912c5b5afe9820`
  olup GitHub asset metadata'sıyla eşleşti
- son kontrolde repository görünürlüğü hâlâ `PRIVATE` idi

Bu doğrulamadan sonra durum matrisi şöyledir:

| Durum | Sonuç | Sınır |
| --- | --- | --- |
| Local complete | Evet | Final package `/tmp/evo-sched-v2-release-84c9e80` altında doğrulandı |
| Committed | Evet | Release tag `84c9e80` evidence-closed commit'ine bağlı |
| Remote published | Evet | Branch, protocol tag'leri ve release private GitHub reposunda erişilebilir |
| Public | Hayır | Repository private; anonim erişim yok |
| Released | Evet, private | Dört asset remote'dan yeniden indirilip checksum ile doğrulandı |

CFP JSON içindeki Additional Resources alanı bu aşamada release PDF URL'sine
çevrilmedi. Sıradaki görünürlük kapısı kullanıcı kararıyla repository veya uygun
bir paper artifact'ini public hale getirmek ve anonim HTTP erişimini doğrulamaktır.
Killer figure private official evidence üzerinden üretilebilir; bu public erişim
kararını ve V3 deneyini bloklamaz.

### 3.67. One-shot kazanımın repeated validation'da kaybolmasını tek figürde gösterdik

KubeCon anlatısının ana görseli paper release'inden ayrı bir presentation asset'i
olarak tasarlandı. Amaç yeni bir sonuç üretmek değil, iki farklı evaluation
derinliğinde elde edilen point-estimate sonuçları aynı görsel dilde karşılaştırmak
ve neden one-shot optimizer sonucunun doğrulanmış kazanım sayılamayacağını tek
bakışta göstermektir.

Figürün veri sözleşmesi şöyledir:

- sol panel official üç-seed optimizer summary ve her seed'in `result-all.csv`
  baseline satırlarını kullanır
- sol panelde üç seed, toplam `216` optimizer adayı ve seçilen beş optimizer-only
  provisional front noktası temsil edilir; hiçbir baseline one-shot front'ta
  kalmamıştır
- sağ panel official finalist-validation `summary.json` dosyasındaki üç baseline
  ve beş optimizer finalistinin üç-workload aggregate repeated means değerlerini
  kullanır
- sağ panel yatay error bar olarak yüzde `95` Student-t mean-p95 confidence
  interval'larını taşır
- repeated-mean point-estimate front yalnızca balanced ve consolidation-heavy
  baseline noktalarından oluşur; beş optimizer finalistinden hiçbiri
  nondominated kalmaz
- paneller farklı latency x-range kullanır; bu fark figür üzerinde açıkça yazılır
  ve right-side confidence interval overlap'i claim sınırı olarak korunur

Figür presentation için ayrı tutuldu:

- source wrapper:
  `presentation/kubecon-europe-2027/killer-figure.tex`
- reusable plot:
  `presentation/kubecon-europe-2027/figures/search-validation-collapse.tex`
- build command: `make killer-figure`
- generated vector PDF:
  `presentation/kubecon-europe-2027/killer-figure.pdf` (Git ignored)

Bu ayrım bilinçlidir. `v2-evidence-2026-08-31` release paper'ı immutable kalır;
KubeCon figürü aynı committed official evidence'tan türetilen sonraki bir sunum
artifact'idir. Paper source veya release asset'i sessizce değiştirilmemiştir.

İlk render'da üç görsel hata bulundu ve source commit öncesinde düzeltildi:

- Pareto çizgi katmanları legend sequence'ine sızdığı için seed marker/label
  eşleşmesi kaymıştı; bu çizgiler `forget plot` ile legend'den çıkarıldı
- sağ paneldeki in-axis count annotation y=3 error bar'larıyla çakışıyordu;
  sayılar kısa panel başlıklarına taşındı
- `addplot+` cycle style error-cap renklerine sızıyordu; explicit `addplot` ile
  baseline ve optimizer interval'ları tutarlı nötr/semantic renklere çekildi

Son render görsel olarak original resolution'da incelendi. Başlık, legend, eksen,
marker, interval ve alt mesaj kutusunda çakışma bulunmadı. Teknik doğrulamalar:

- `make killer-figure` geçti
- standalone PDF tek sayfa ve `qpdf --check` temiz
- bütün fontlar embedded Type 1
- LaTeX log'da warning, undefined reference veya overfull/underfull box yok
- official JSON sanity sorgusu `3` seed, `216` candidate, `5` provisional point,
  `0` seed with baseline survival ve `5` finalist değerlerini yeniden üretti
- repeated summary sanity sorgusu `8` policy, `5` optimizer policy, `2` front
  point ve `0` optimizer front member değerlerini yeniden üretti
- genel `make verify` yeniden geçti

Figür kaynakları `9691136 docs: add noisy-fitness collapse figure` commit'inde
sabitlendi. README'ye build komutu ve immutable-release ayrımı eklendi. Sıradaki
KubeCon sunum işi bu figürü merkez alan 30 dakikalık talk outline'ı ve 3--5
dakikalık İngilizce speaking sample script'idir. Public artifact visibility kararı
ayrı gate olarak açık kalır; V3 bu sunum paketini bloklamaz.

### 3.68. KubeCon talk omurgasını ve speaking sample script'ini sabitledik

Killer figure tamamlandıktan sonra CFP'deki yedi satırlık high-level outline,
sunum üretimine doğrudan girdi olacak slide-level pakete dönüştürüldü. Paper
abstract'ını slaytlara bölmek yerine practitioner odaklı bir reversal anlatısı
seçildi:

1. daha iyi policy bulduğumuzu sandık
2. live evaluation loop ve gerçek Pod binding yolunu gösterdik
3. V1 policy-space ve measurement risklerini audit ettik
4. identifiable V2 policy ve timestamp provenance'ını kurduk
5. üç seed'in one-shot sonucunu olumlu görünen provisional aşama olarak sunduk
6. ten-repeat validation ile beş finalistin front'ta kalmadığını gösterdik
7. holdout baseline tradeoff'u ile optimizer failure ve gerçek policy surface'i
   birbirinden ayırdık
8. sonucu live-control-loop tuning checklist'ine çevirdik

Sunum zaman bütçesi `25` dakika içerik ve `5` dakika audience questions olarak
kilitlendi. CFP JSON outline'ı bu bütçeye göre güncellendi; son içerik slide'ı
`23:30-25:00`, soru bölümü `25:00-30:00` aralığındadır. Mevcut CFP başlığı
`Searching the Kubernetes Scheduler Policy Space: Lessons From Noisy Live
Optimization` korundu. `When the Optimizer Learns the Noise: Lessons From Live
Kubernetes Scheduler Tuning` sunum/video için daha hikaye odaklı alternatif
olarak pakette tutuldu, fakat CFP başlığı sessizce değiştirilmedi.

[Talk package](presentation/kubecon-europe-2027/talk-package.json) şu sözleşmeyi
taşır:

- `11` slide için süre, amaç, visual ve speaker point
- official optimizer, repeated-validation, holdout ve paper-audit source path'leri
- universal superiority, statistical significance, powered-node cost ve
  production generalization iddialarını engelleyen claim boundaries
- `4:00` hedefli, yedi segment ve `423` kelimelik İngilizce speaking sample
- camera -> architecture -> killer figure -> measurement -> holdout -> camera
  recording akışı
- video, anonymous artifact visibility, speaker bio/experience ve final
  Sessionize check için açık gate'ler

Speaking sample paper özeti gibi yazılmadı. İlk `25` saniyede deneysel reversal
verilir; one-shot panel `216` aday ve beş provisional point ile açıklanır;
repeated panelde sekiz policy, ten-repeat/workload protokolü, overlapping yüzde
`95` interval'lar ve `0/5` optimizer-front sonucu belirtilir. Sonuç scheduler'a
özgü bir başarı iddiası yerine identifiable policy, timestamp provenance, hard
validity gate, repeated promotion ve immutable artifact checklist'ine bağlanır.

Repository private kaldığı için CFP `additionalResources` girdileri silinmedi,
fakat üçü de `availability: private-pending-public-release` ve
`submissionReady: false` olarak işaretlendi. Böylece owner hesabında çalışan URL,
reviewer için erişilebilir public resource gibi yanlış raporlanmayacaktır.

Doğrulama:

- CFP ve talk package `jq empty` kontrolünden geçti
- talk package `11` slide, `25+5=30` dakika ve `7` speaking segment raporladı
- script word-count sanity check'i `423` ile tanımlı `420-470` aralığında geçti
- üç additional resource girdisinin tamamı `submissionReady=false`
- `git diff --check` temiz

Paket `d57bf17 docs: add KubeCon talk package` commit'inde sabitlendi. README'ye
talk-package yolu eklendi. Bu noktada automated olarak hazırlanabilecek sunum
metni tamamdır; sıradaki gerçek kullanıcı gate'i İngilizce videoyu kaydetmek,
izlemek ve anonim erişilebilir unlisted/public URL üretmektir. Public repo/paper
visibility kararı da CFP submission'dan önce ayrıca kapanmalıdır. V3 hâlâ CFP
paketinin ön koşulu değildir.

### 3.69. Noise-aware V3 için budget-matched protokolü implementation'dan önce yazdık

V2'nin ana negatif sonucu, one-shot search fitness'ının shared repeated
validation altında güvenilir promotion sağlamadığını gösterdi. Bundan sonraki
yüksek değerli fakat CFP'yi bloklamayan araştırma sorusu, aynı live-evaluation
bütçesinin bir bölümünü search-time resampling'e ayırmanın false Pareto discovery
oranını düşürüp düşürmediğidir.

V2 evidence geçmişini değiştirmemek için yeni branch
`research/noise-aware-resampling-v3`, güncel V2/KubeCon commit'i `cc26a0f`
üzerinden açıldı. V2 protocol tag'leri, raw results, paper ve
`v2-evidence-2026-08-31` release asset'leri immutable kalacaktır. V3 kodu,
protocol tag'leri ve result dizinleri ayrı olacaktır.

Source audit sırasında kritik implementation engeli bulundu: mevcut
`clusterEvaluator`, normalized weight key'ini cache'liyor. Aynı policy tekrar
çağrıldığında live workload replay yerine cached `EvaluationData` dönüyor. Bu V2
one-shot phenotype dedup davranışı için uygundur, fakat V3 resampling değildir.
Bu nedenle V3 fresh-sample semantiği cache bypass, unique evaluation ID, sample
ordinal, timestamps ve cumulative budget provenance'ı olmadan uygulanmayacaktır.

[V3 protocol](experiments/protocol-v3-budget-matched-resampling.json) şu
official search bütçesini sabitler:

- one-shot control: `12 + 5 x 12 = 72` optimizer sample
- two-stage treatment: `12 + 5 x (6 yeni + 2 promoted x 3 ek) = 72` optimizer
  sample
- iki arm için ayrıca aynı üç one-shot baseline reference
- her sample aynı üç train workload'u içerir
- arm/seed başına `75` total policy sample, `225` workload execution ve `5,400`
  Pod
- üç seed ve iki arm için official search toplamı `1,350` workload execution ve
  `32,400` Pod

Eşit bütçenin doğal tradeoff'u açık tutuldu: one-shot arm en çok `72`, two-stage
arm en çok `42` unique candidate keşfeder. Treatment her generation altı yeni
offspring üretir; current population ve offspring pool'unda lower rank, higher
crowding ve creation ordinal sırasıyla seçilen daha önce promote edilmemiş iki
candidate üç ek fresh sample alır. Candidate fitness dört sample-level objective
point estimate'in aritmetik ortalamasıyla yeniden hesaplanır; rank/crowding daha
sonra tekrar kurulur. Eligible candidate sayısı yetersizse bütçe başka yere
kaydırılmaz, arm fail olarak korunur.

Pilot paper evidence değildir ve daha küçük budget kullanır:

- one-shot: `population=4`, `generation=1`, `4+4=8` optimizer sample
- treatment: `4 + 2 yeni + 1 promoted x 2 ek = 8` optimizer sample
- her arm üç baseline ile birlikte `11` policy sample, `33` workload execution
  ve `792` Pod
- acceptance exact budget, promoted candidate'ta üç distinct live evaluation,
  mean aggregation, cache bypass ve clean postflight'i doğrular

Official primary outcome
`falseParetoDiscoveryProportion` olarak tanımlandı: shared ten-repeat validation'a
alınan provisional optimizer finalistlerinden repeated-mean point-estimate
front'ta kalmayanların oranı. Treatment ancak eşit search sample bütçesinde bu
oran control'den düşükse ve safety metric'ler geçerse descriptively desteklenmiş
sayılacaktır. Eşit/yüksek oran, iki arm'da da aday çıkmaması veya hiçbir finalistin
kalmaması geçerli negatif sonuçtur. Üç seed ile hypothesis-test iddiası
yapılmayacaktır.

Shared validation arm başına en çok beş exact-phenotype-deduplicated finalisti ve
üç baseline'ı on fresh repeat/workload ile ölçer. Candidate sayısı outcome'a bağlı
olduğu için validation bütçesi search bütçesinden ayrı raporlanacaktır. V3
protocol holdout içermez; nondominated kalan yeni bir optimizer policy çıkarsa
genelleme testi için ayrıca committed holdout protocol gerekir.

Protocol JSON kontrolleri:

- `jq empty` geçti
- official control ve treatment bütçeleri yeniden hesaplandığında `72/72`
- arm/seed workload ve Pod hesabı `225` ve `5,400`
- pilot arm bütçeleri `8/8`, workload ve Pod hesabı `33` ve `792`
- valid-negative, fresh-validation, V2 immutability ve pilot-not-evidence
  sözleşmeleri machine-readable olarak doğrulandı

Protocol `462c443 docs: define budget-matched V3 protocol` commit'inde,
implementation başlamadan önce kaydedildi. Statüsü official freeze değildir;
pilot manifest alanları implementation sonrasında gözden geçirilebilir. Ancak
official arm sample bütçeleri, primary outcome ve valid-negative kuralları sonuç
görüldükten sonra değiştirilmeyecektir. Sıradaki adım test-first fresh evaluation,
sample aggregation, deterministic promotion ve budget accounting kodudur; cluster
pilot ancak bu kod clean commit ve acceptance testlerinden sonra açılacaktır.

### 3.70. V3 fresh-sample optimizer ve budget gate'lerini V2 yolunu bozmadan ekledik

V3 implementation üç ayrı atomik kod commit'inde ilerledi:

- `1144af3 feat: add budgeted optimizer sampling`
- `cd1712c feat: expose fresh budgeted optimizer runs`
- `f0e0caf feat: gate budgeted multi-seed experiments`

İlk commit optimizer domain katmanını ekledi. Mevcut `optimizer.Run` ve cache'li
V2 evaluation yolu değiştirilmedi; V3 yalnızca ayrı `RunBudgeted` entrypoint'i
üzerinden çalışır. `budget-one-shot` ve `budget-two-stage` modları exact sample
budget denklemini başlamadan doğrular. Treatment'ta promotion sırası lower
nondomination rank, higher crowding distance ve lower creation ordinal olarak
deterministiktir. Her başarılı live replay ayrı `EvaluationSample` olarak şu
provenance ile tutulur:

- unique evaluation ID, sample kind ve sample ordinal
- baseline/optimizer budget class ve budget ordinal
- fresh sample start/finish timestamp'leri
- sample-level workload metric'leri ve iki objective
- sample sonrasındaki candidate aggregate objective
- cumulative attempted optimizer sample ve workload execution sayısı

Promoted candidate'ın objective'i sahip olduğu fresh sample point estimate'lerinin
aritmetik ortalamasıdır. Promotion'ın ikinci/sonraki sample'ı fail olursa daha
önce tamamlanan sample result'tan kaybolmaz. Partial result ayrıca attempted ve
actual sample/workload bütçelerini, bitiş zamanını ve o ana kadar oluşmuş unique
candidate sayısını korur. Bu davranış özellikle evaluator hatası, terminal
kesintisi veya reboot sonrasında denominator'ın geriye dönük tahmin edilmesini
önlemek için eklendi.

İkinci commit `cmd/optimizer` CLI katmanını bağladı. `--search-mode` boşken eski
V2 `optimizer.Run` ve phenotype cache aynen kullanılır. `budget-one-shot` veya
`budget-two-stage` verildiğinde `EvaluateFresh` her çağrıda gerçek
`EvaluateWorkloads` replay'i yapar; aynı canonical weight tekrar gelse bile cache
hit sample sayılamaz. Yeni CLI contract'ı şunları içerir:

- `--search-mode`
- `--offspring-per-generation`
- `--promotions-per-generation`
- `--additional-samples-per-promotion`
- `--optimizer-sample-budget`
- opsiyonel `--samples-csv-output`

Budgeted JSON çıktısına ek olarak otomatik `result-samples.csv` üretilir. Bu CSV
mode ve seed'i açık kolonlarda; candidate identity/weights, evaluation ID,
sample/aggregate objectives, timestamps ve cumulative budget ile taşır. Candidate
aggregate CSV ile raw fresh-sample CSV bilinçli olarak ayrıdır; promoted
candidate'ın workload satırlarını tek bir koşuymuş gibi göstermiyoruz.

Üçüncü commit mevcut `multi-seed-runner`ı kopyalamak yerine geriye uyumlu şekilde
genelleştirdi. V2 default command ve official evidence eligibility testle
korundu. V3 için runner artık unique candidate ile fresh policy sample'ı ayrı
hesaplar ve tamamlanmış bir arm'ı ancak şu gate'ler birlikte geçerse kabul eder:

- JSON search mode ve seed beklenen değerlerdir
- optimizer, baseline, workload execution ve promotion bütçeleri expected,
  attempted ve actual olarak eşittir
- JSON'daki evaluation ID'ler non-empty ve unique'tir
- candidate `evaluationCount` değeri raw sample sayısıyla eşittir
- `result-samples.csv` evaluation ID kümesi JSON ile birebir aynıdır
- resolved workload'lardan hesaplanan Pod bütçesi raw sample workload
  metric'lerinden yeniden toplanınca aynıdır
- result, Pareto CSV, all-candidates CSV, sample CSV ve run log checksum'ları
  manifest'e girer

Pilot/two-stage örneğinde bu ayrım `9` unique candidate (`6` optimizer + `3`
baseline) ve `11` fresh policy sample (`8` optimizer + `3` baseline) demektir.
Üç workload ile runner `33` workload execution ve resolved inputlara göre `792`
Pod bekler. Pilot ve henüz shared validation görmemiş budgeted official search
completion anında otomatik olarak `paperEvidenceEligible=false` kalır.

Failed veya interrupted subprocess'te completion gate uygulanmaz; bulunan
partial JSON/CSV/log artifact'leri best-effort SHA-256 ile manifest'e bağlanır ve
beklenen denominator manifestte korunur. Cluster recovery canonical weights,
clean evaluator namespace, expected worker count ve experiment cadence için
mevcut preflight/postflight sözleşmesini kullanmaya devam eder.

Doğrulamalar:

- optimizer ve runner package testleri geçti
- ilgili package'lerde race detector geçti
- tüm `go test ./...` ve `go vet ./...` geçti
- optimizer budget testi 100 kez tekrarlı çalıştırıldığında deterministik kaldı
- `make verify` üç implementation commit'i sonrasında da official V2 evidence,
  generated analysis ve paper zincirini temiz doğruladı

Bilerek açık bırakılan gözlemlenebilirlik noktası scheduler rollout sayısıdır.
Her fresh sample policy apply/restore akışını çağırsa da deployment gerçekten yeni
Pod üretmediğinde bunu gerçekleşmiş rollout diye saymak doğru değildir. Exact
sample, workload ve Pod bütçesi şu an kanıtlanmaktadır; gerçek rollout/update
telemetry'si gerekiyorsa evaluator tarafında ayrıca instrument edilmeden rapora
eklenmeyecektir.

Bu bölüm yazılırken kind cluster hâlâ durdurulmuştur ve hiçbir V3 live pilot
başlatılmamıştır. Sıradaki adım iki arm'ı `/tmp` altında sabit sırayla çalıştıran
pilot launcher/manifest katmanını eklemek, clean pilot commit/tag oluşturmak ve
ancak preflight sonrasında cluster'ı açmaktır.

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
- `seed=123` ve `seed=999` historical V1 protokolünde çalıştırılmadı; bunun yerine `42`, `123`, `999` seed'lerinin tamamı V2 official protokolünde çalıştırıldı
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
- Gate 4 official baseline protokolü tamamlanmıştır
- Gate 5 official `42`, `123`, `999` multi-seed search tamamlanmış ve beş optimizer validation finalisti çıkarılmıştır
- Üç seed toplamında `216` optimizer adayı, `9` baseline evaluation, `675` workload execution ve `16,200` Pod başarıyla ölçülmüştür
- Her üç seed'in tek-evaluation combined Pareto front'unda baseline'lar düşmüş, fakat üstünlük iddiası repeated finalist validation sonrasına ertelenmiştir
- Seed başına unique Pareto nokta sayıları `2`, `2`, `1` olduğu için strict interior knee point bulunmamaktadır
- V2 multi-seed summary JSON/CSV ve provenance manifesti commitlenmiştir
- Beş finalist ve üç baseline için `n=10` repeated validation policy dosyası ve official runner hazırlanmıştır
- Repeated summary istatistiklerine yüzde `95` Student-t confidence interval eklenmiştir
- Holdout temsilci seçim kuralı train validation sonucu görülmeden önce sabitlenmiştir
- Sekiz policy için `n=10` repeated train validation 240/240 satır ve 5,760/5,760 Pod ile tamamlanmıştır
- Tek-koşu optimizer kazanımı repeated-mean front'ta yeniden üretilememiş; beş optimizer finalistinin tamamı baseline'lar tarafından domine edilmiştir
- Repeated-mean Pareto front balanced pending-best ve consolidation-heavy node-best noktalarından oluşmaktadır
- Pooled confidence interval'lar çakıştığı için point-estimate dominance istatistiksel üstünlük olarak yorumlanmamaktadır
- Önceden sabitlenen holdout seti duplicate removal sonrası üç V2 baseline'a indirgenmiştir
- Official holdout üç baseline x on tekrar ve 720/720 tamamlanan Pod ile acceptance'ı geçmiştir
- Holdout repeated-mean front'u spread-heavy, balanced ve consolidation-heavy için sırasıyla 4, 3 ve 2 occupied-worker kullanan üç unique noktadan oluşmuştur
- Holdout interior knee point'i `evok8s-v2-balanced` olarak seçilmiştir
- Holdout latency confidence interval'ları çakıştığı için latency sıralaması descriptive point estimate olarak tutulmaktadır
- Gate 5 tamamlanmış, Gate 6 paper-evidence alt-fazı tamamlanmıştır
- Final V2 paper source'u `549e272` commit'inde sabitlenmiş; altı sayfalık IEEE conference PDF'ye derlenip görsel/sayısal denetimden geçmiştir
- V2 paper evidence audit `9b678f0` commit'inde `10/10` bulgu resolved ve submission evidence ready olarak kapatılmıştır
- Public İngilizce README, Apache-2.0 lisansı ve pinned tested-version bilgisi eklenmiştir
- `make verify` Go test/vet, official summary regeneration, evidence audit ve paper PDF kontrollerini tek komutta geçmiştir
- V2 branch, dört protocol tag ve checksum doğrulamalı official evidence release'i private GitHub reposunda yayımlanmıştır
- Repository private olduğu için release PDF henüz CFP'de anonim erişilebilir stabil public kaynak değildir
- One-shot provisional front ile ten-repeat validation çöküşünü yan yana gösteren standalone KubeCon vector figure hazırdır
- 25 dakikalık içerik, 5 dakikalık soru bölümü ve 4 dakikalık İngilizce speaking sample script'i içeren KubeCon talk package hazırdır
- V3 noise-aware search için V2'den ayrılmış research branch ve budget-matched machine-readable protocol hazırdır
- V3 fresh-sample optimizer, cache-bypass CLI, sample CSV ve exact-budget runner gate'leri testli ve commitlidir
- V3 iki-arm pilot launcher'ı ve live pilot henüz tamamlanmamıştır; cluster kapalıdır
- Functional live smoke ve kind resource lifecycle komutları Makefile entrypoint'leri olarak eklenmiştir
- Gate 6 CFP taslağı official V2 claim setine bağlı ve commitlidir; public paper
  PDF, speaking sample, kullanıcı teyitleri ve Sessionize gönderimi açıktır
- Scheduler interval testli admin komutuyla canonical `2s` değerine restore edilmiştir
- `evo-sched` kind container'ları Gate 5 search postflight sonrasında silinmeden durdurulmuştur
- Scheduler experiment preflight skill'i V2 contract, canonical weights ve exact worker-count kontrolüyle güncellenmiştir
- Güncellenen preflight helper'ın gerçek-cluster acceptance testi geçmiştir
- Measurement `v2` timestamp semantiği source audit ile tanımlanmıştır
- Evaluator polling'in pending timestamp kaynağı olmadığı; cadence, second-level Kubernetes time ve observation lag'in ayrı riskler olduğu netleştirilmiştir
- Measurement `v2` high-resolution submission/decision instrumentation'ı uygulanmıştır
- Evaluator output'u decision latency, coarse Kubernetes pending ve observation lag kaynaklarını ayrı taşımaktadır
- Optimizer objective'leri decision latency ve workload-occupied worker count olarak sürümlenmiştir
- Eksik, kısmi, unschedulable veya protocol-drift içeren evaluation'lar objective üretmeden reddedilmektedir
- Scheduler ve evaluator cadence değerleri JSON/CSV provenance'ına eklenmiştir
- Gate 2 kod doğrulamaları race detector ve `go vet` ile geçmiştir
- Gate 2 live measurement smoke ve üç tekrarlı cadence pilotu tamamlanmıştır
- `2s` cadence'in `250ms` cadence'e göre mean decision latency'yi `4.11x`, p95'i `4.21x` artırdığı pilotta gözlenmiştir
- Polling primary decision objective'inden ayrıştırılmış; watch/event implementation'ı koşullu future work olarak bırakılmıştır
- Gate 2 tamamlanmıştır
- Gate 3 requested-resource support metric kodu ve V2 pressure workload suite'i commitlenmiştir
- V2 `cpu-tight-train` debug live smoke'u `24/24` completion, 123 source-tagged resource sample ve temiz postflight ile tamamlanmıştır
- İlk V2 baseline pilotu scheduler rollout overlap yarışı nedeniyle geçersiz sayılmış; deployment `Recreate` ve strict rollout convergence ile düzeltilmiştir
- Üç V2 baseline x üç train workload pilot acceptance matrix'i dokuz kombinasyonda tam completion ve policy separation ile tamamlanmıştır
- Default kube-scheduler için deployment patch yapmayan external-reference evaluator yolu ve live debug smoke tamamlanmıştır
- Evaluator custom ve external baseline'ları tek komutta round-robin tekrarlayıp tek output'ta birleştirebilmektedir
- Üç V2 train workload üzerinde üç custom policy ve default kube-scheduler için üç tekrarlı official baseline protokolü tamamlanmıştır
- Official baseline artifactleri, manifestleri ve mean/sample-SD/worst-case özetleri commitlenmiştir
- Clean/tag/preflight hard gate'li ve interruption recovery destekli repo-içi official multi-seed runner tamamlanmıştır
- Sıradaki iş stabil public paper PDF ve speaking sample hazırlayıp CFP taslağını
  kullanıcı onayıyla Sessionize'a aktarmaktır

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
- Adım 8'in V2 multi-seed search, repeated train validation ve holdout baseline genelleme fazları tamamlandı; optimizer-only kazanım doğrulanmadı, workload-duyarlı baseline tradeoff'u doğrulandı

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
- V2 preflight helper güncellendi; gerçek-cluster acceptance testi Gate 2 live measurement smoke öncesinde yapılacak

### Gate 2 - Measurement `v2`

- [x] Scheduler loop interval'ini `SCHEDULER_INTERVAL` ile configurable yap
- [x] Evaluator submission ve scheduler decision anlarını high-resolution annotation olarak kaydet
- [x] Kubernetes condition zamanı, scheduler decision zamanı ve evaluator observation zamanını ayır
- [x] Measurement protocol version ve timestamp source metadata'sını output'a ekle
- [x] `maxActiveNodes` adını `maxWorkloadOccupiedWorkerNodes` olarak düzelt
- [x] Scheduler'ın `2s` cadence etkisini tiny-control deneyle ölç
- [x] Gerekirse event/watch tabanlı monitoring'i polling'e karşı değerlendir

Çıkış koşulu:

- Aynı policy tekrarlarında ölçüm tabanı, jitter ve timestamp çözünürlüğü raporlanabilir olmalı

Sonuç:

- Gate 2 çıkış koşulları tamamlandı
- measurement baseline, pooled jitter ve timestamp source ayrımı raporlanabilir durumda
- event/watch şu an uygulanmayacak; occupancy resolution ihtiyacı değişirse karar yeniden açılacak

### Gate 3 - Workload ve metrik seti `v2`

- [x] Workload başına Pod sayısını p95 ve dağılım analizi için 24'e çıkar
- [x] CPU-tight, mem-tight ve complementary resource mix rejimlerini portable capacity pressure ile tanımla
- [x] Optimizer'ın görmediği bir holdout workload tanımla
- [x] `unschedulable` değerini safety/feasibility kısıtı olarak koru
- [x] Requested utilization, dominant-load variance ve CPU-memory imbalance destek metriklerini ekle
- [x] Polling adequacy ratio ve resource metric source provenance'ını output contract'a ekle
- [x] Tek V2 baseline ile 24-Pod debug live smoke çalıştır
- [x] Üç V2 baseline x üç train workload pilot acceptance matrix'ini çalıştır
- [x] Workload checksum ve instantiate edilen mutlak resource request'lerini official manifestte sakla

Çıkış koşulu:

- Policy farkları yalnızca birkaç Pod veya tek cadence tick'ine bağlı olmadan ölçülebilmeli

Sonuç:

- Gate 3 acceptance tamamlandı
- pilot manifest workload checksum ve mutlak request'leri taşır; aynı provenance
  official tekrarlı koşul manifestlerinde de zorunlu tutulacaktır
- tek-run pilot değerleri final paper claim'i değildir

### Gate 4 - Baseline protokolü

- [x] V2 balanced, spread-heavy ve consolidation-heavy presetlerini tanımla
- [x] Default kube-scheduler için external-reference execution ve output contract'ı ekle
- [x] Event-driven kube-scheduler ile polling custom scheduler arasındaki ham latency karşılaştırmasının sınırını açıkla
- [x] Placement ve consolidation metriklerini ortak karşılaştırma zemini olarak önceliklendir
- [x] Default kube-scheduler external-reference debug smoke'unu clean commit üzerinden çalıştır
- [x] Custom ve external baseline'lar için tek komutlu repetition desteği ve live debug smoke ekle
- [x] Üç V2 train workload üzerinde tekrarlı official default kube-scheduler baseline'ı çalıştır
- [x] Üç custom V2 baseline'ı aynı repetition ve manifest protokolüyle official çalıştır
- [ ] Uygunsa kube-scheduler `NodeResourcesFit` / `MostAllocated` packing referansını ayrıca ölç

Çıkış koşulu:

- Her baseline aynı workload, cluster ve raporlama protokolü altında izlenebilir olmalı

Sonuç:

- Gate 4 çıkış koşulu tamamlandı
- `MostAllocated` karşılaştırması opsiyonel genişletme olarak ertelendi
- Gate 5 official optimization ve doğrulama artık sıradaki aktif kapıdır

### Gate 5 - Official optimization ve doğrulama

- [x] Repo-içi official multi-seed runner ile clean SHA/tag, input checksum, preflight, seed log ve postflight provenance'ını zorunlu tut
- [x] Clean stable commit ve annotated protocol tag oluştur
- [x] Preflight sonrasında `42`, `123`, `999` seed'lerini sequential çalıştır
- [x] Her seed için unique nondominated objective noktalarını çıkar
- [x] Beş optimizer finalisti ile üç baseline'ı tek canonical validation dosyasında sabitle
- [x] Clean/tag/checksum/preflight ve automatic cadence restoration içeren repeated validation runner ekle
- [x] Mean/SD/worst-case özetine yüzde 95 confidence interval ekle
- [x] Finalist Pareto çözümlerini ve baseline'ları bağımsız tekrarlarla yeniden ölç
- [x] Merkezi eğilim yanında dağılım, worst-case ve güven aralığı raporla
- [x] Pareto yapısını all-measured solutions üzerinden yeniden kur
- [x] Pending-best, node-best ve yeterli nokta varsa knee-point temsilcisini seç
- [x] Holdout workload üzerinde genelleme kontrolü yap

Çıkış koşulu:

- Optimizer-only kazanım repeated validation ile ya yeniden üretilmeli ya da
  reddedilmeli; seçilmiş baseline tradeoff'u bağımsız holdout üzerinde ölçülmeli
  ve bütün official kanıt zinciri commitlenmiş olmalı

Sonuç:

- Gate 5 tamamlandı
- single-evaluation optimizer kazanımı repeated train validation'da reddedildi
- bağımsız holdout üç baseline için üç noktalı Pareto tradeoff'u ve balanced knee
  üretti
- Gate 6 artık sıradaki aktif kapıdır

### Gate 6 - Paper, repo ve CFP hazırlığı

- [x] `paper-evidence-audit` ile her sayısal claim'i official artifact'e bağla
- [x] Abstract, methods, results, discussion ve conclusion'ı yalnızca final protokol sonuçlarıyla güncelle
- [x] Official V2 uncertainty, baseline, repeated-validation ve holdout bilgisini grafiklerde görünür yap
- [x] Final PDF'yi derle, altı sayfayı görsel incele ve evidence audit'i commit SHA ile kapat
- [x] Statistical-test çağrışımlı claim dilini point-estimate Pareto sözleşmesine göre düzelt
- [x] Small-sample p95 limitation'ını ve pre-specified protocol dilini paper ile evidence audit'e ekle
- [x] En yakın multi-objective Kubernetes scheduling çalışmalarını primary kaynaklarla doğrula ve novelty claim'ini noisy live fitness validation eksenine daralt
- [x] README, lisans, pinned sürümler ve tek komutlu reproduction akışı ekle
- [x] CFP anlatısını "evrensel en iyi scheduler" yerine canlı cluster üzerinde policy-space search sırasında öğrenilen tradeoff ve metodoloji dersleri üzerine kur
- [x] V2 branch, protocol tag'leri, paper ve official evidence asset'lerini private release olarak yayımla ve remote checksum doğrulaması yap
- [x] One-shot apparent gains ile repeated-validation sonucunu aynı evidence figure'da karşılaştır
- [x] 30 dakikalık KubeCon talk outline'ı ve 3--5 dakikalık speaking sample script'i hazırla
- [ ] Stabil public paper PDF, speaking sample ve kullanıcı teyitlerini tamamla
- [ ] Sessionize formunu son resmi gereksinim kontrolünden sonra gönder

Çıkış koşulu:

- Repo, paper ve CFP metni aynı commitlenmiş kanıt setini referans göstermeli
- Dış submission artefact'leri tamamlanmalı ve gönderim kullanıcı tarafından
  onaylanmalı

### Gate 7 - Optional noise-aware V3

- [x] V2 release ve result immutability sınırını sabitle
- [x] One-shot ve two-stage arm'lar için eşit fresh-sample bütçesini tanımla
- [x] Primary false-discovery outcome, valid-negative ve finalist cap kurallarını yaz
- [x] Küçük pilot bütçesi ve acceptance koşullarını machine-readable protocol'e koy
- [x] V2 one-shot yolunu bozmadan fresh evaluation API ve sample provenance ekle
- [x] Test-first deterministic promotion, mean aggregation ve exact budget accounting uygula
- [ ] Pilot runner'ın clean/tag/checksum/preflight/postflight hard gate'lerini ekle
- [ ] Clean pilot protocol commit/tag oluştur
- [ ] İki arm'lı pilotu koştur ve acceptance'ı kapat
- [ ] Pilot sonucuna göre yalnızca implementation-derived manifest alanlarını gözden geçir
- [ ] Official protocol tag ve yeni result dizinlerini sabitle
- [ ] Counterbalanced üç-seed, iki-arm official search'ü koştur
- [ ] Shared ten-repeat finalist validation ve false-discovery analizini tamamla

Çıkış koşulu:

- iki arm aynı live search sample budget'ını tüketmeli
- every repeated sample gerçek cluster replay'i olmalı; cache hit sample sayılmamalı
- pilot çıktıları official evidence olarak etiketlenmemeli
- mitigation işe yaramasa bile sonuç ve partial artifact zinciri korunmalı
- V3 hiçbir aşamada KubeCon CFP submission gate'ini bloklamamalı

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
