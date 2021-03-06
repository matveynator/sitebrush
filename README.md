# SiteBrush: Удобный редактор сайта. 

<img src='https://repository-images.githubusercontent.com/429163995/331b95fa-4309-4d25-8c1a-0e8f34ff7b25' align="right">

# Installation:

* Install Apache, PHP, MySQL, Memcached or Redis.
* clone repo [https://github.com/matveynator/SiteBrush/archive/refs/tags/v1.0.zip](https://github.com/matveynator/SiteBrush/archive/refs/tags/v1.0.zip)
* create MySQL database from schema: SiteBrush/sitebrush.sql
* edit SiteBrush/public_html/config.php 


# Установка одной командой Debian 10, 11: 
после установки вся важная информация (пути и пароли) записывается в файл: /etc/info

```
curl -L https://raw.githubusercontent.com/matveynator/sitebrush-docker/main/install.sh | sudo bash
```

## Demo: [sitebrush.com](http://sitebrush.com)

Мы убеждены, что Вы имеете право редактировать Ваш сайт целиком, не держа при этом в голове кучу лишней информации и возможно ненужных знаний таких как ftp доступы, теги вставки содержимого различных CMS, парадигмы редактирования шаблонов разных CMS, расположение файлов на сервере и многого другого чем мучают современных вебмастеров. Никто не против чтобы Вы все это знали, sitebrush лишь решает рутинные технические вопросы, чтобы Вы могли сосредоточиться на ... творчестве!

Динамические сайты управляемые большинством современных "систем управления сайтами"(по английски CMS) утратили мощь и гибкость редактирования статических страниц визуальными редакторами сайтов наподобе Dreamweaver или просто Блокнот или Vim. Современные CMS вместо полного доступа к редактированию страницы обычно предлагают Вам отредактировать по-отдельности "шаблоны" и "содержимое". Это неудобно по следующим причинам:

* Во время редактирования не видно конечного результата, потому что Вы редактируете либо шаблон либо содержимое которое будет вставлено в этот шаблон.
* Вам необходимо изучить технологию на котором написан шаблон сайта, понимать скудные теги используемые при редактировании содержимого. 
* Все страницы вашего сайта выглядят однообразно. Чтобы сделать страницы с разным оформлением - Вам проще сделать статический сайт.
* Плодятся одинаковые дизайны сайтов потому, что шаблоны чудовищно сложны для редактирования - проще копировать чужой шаблон.
* Динамический сайт работает медленнее чем статический и создает несоизмеримо большую нагрузку на сервер. 

Статические сайты редактируемые программами наподобе "dreamveawer" или в "блокнотe/vim" тоже имеют серьезные недостатки:

* Чтобы внести изменение - необходимо "скачать" "отредактировать" и "выложить" страницу и все сопутствующие файлы, изучив технологию наподобе ftp.
* Вам надо редактировать каждую страницу по отдельности. Допустим если у вас поменялся телефон, а у вас 1000 страниц на сайте где он упомянут - прийдется вручную поменять все 1000 страниц.
* Вы не можете редактировать сайт из-за любого компьютера или совместно с другими людьми через сеть интернет.

Поэтому появился SiteBrush, который совмещает лучшее из обоих миров: динамических и статических сайтов. Мы сделали его для себя, а Вам он обязательно понравится!

## Screenshots:

<img src="http://sitebrush.com/f/389b73b76b94f91f86fd942b64ee4686.png" width="400"> <img src="http://sitebrush.com/f/1056d0a4560056ede806c06ed818bd1e.png" width="400"> 

## Основные возможности: 
* Вам ненужен хостинг для вашего сайта (sitebrush и есть хостинг).
* Ваш сайт с Вашим доменным именем.
* Редактирование сайта в режиме "онлайн".
* Удобство визуального редактора ckeditor или редактирования в чистом html. 
* Любой html css или javascript.
* Версии страниц с возможностью возврата (защита от ошибок).
* Безопасная загрузка любых файлов размером до 512 Мбайт.
* Обработка: уменьшение/поворот всех типов изображений в высоком качестве (даже анимированных gif).
* Ежедневное резервное копирование сайта (вы получаете архив который можно развернуть на любом web сервере).
* Высочайшая скорость работы Вашего сайта.

## Это есть только в sitebrush:
* Магия номер 1:  "Повторяющиеся элементы". Статический сайт не может, а sitebrush может сделать повторяющиеся на всех страницах элементы которые можно удобно редактировать. Чтобы отделить повторяющиеся элементы - например навигацию, контактную информацию, таблицы стилей и многое другое - задайте этим элементам class="SiteBrush-Template TemplateID", где TemplateID любое уникальное слово или комбинация слов. Изменённый на любой странице сайта такой элемент поменяется на всех остальных страницах где он присутствует - автоматичеcки. Неподготовленный человек даже не будет понимать как это произошло, а Вам это обязательно понравится!

* Магия номер 2:  "Автоматический импорт всех файлов". Вы даже не заметите как sitebrush автоматически перенесет все файлы входящие в состав редактируемой страницы из внешних источников - внутрь вашего сайта. Картинки, javascript, таблицы стилей, видеоролики, а так же любые другие файлы будут автоматически перенесены из внешних источников на ваш сайт и тем самым никогда не потеряются. Вам это тоже обязательно понравится!

* Магия номер 3:  Если страница меняет адрес допустим /contacts меняется на /address, то она доступна по старому и новому адресу автоматически. Таким образом Ваши посетители никогда не увидят ошибок "404" (страница не найдена), ссылки с других сайтов всегда будут приводить к нужной странице и поисковые машины (google/yandex и тп) будут сохранять достигнутый вашим сайтом высокий рейтинг. Вам это обязательно понравится!

* Магия номер 4:  "Заморозка". Нажимая на кнопку "заморозка" последующие изменения на сайте будут видно только Вам. Ваши посетители не увидят досадных ошибок пока Вы все не перепроверите и не опубликуете окончательную версию. Вам это обязательно понравится! 

